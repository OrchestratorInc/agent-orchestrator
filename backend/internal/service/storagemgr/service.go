package storagemgr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/sentryobs"
	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// Store is the durable surface the service needs.
type Store interface {
	GetStorageSettings(ctx context.Context) (domain.StorageSettings, error)
	SaveStorageSettings(ctx context.Context, settings domain.StorageSettings, now time.Time) error
	GetSessionStorageFacts(ctx context.Context, id domain.SessionID) (domain.SessionStorageFacts, bool, error)
	ListSessionStorageFacts(ctx context.Context) (map[domain.SessionID]domain.SessionStorageFacts, error)
	RecordSessionOffload(ctx context.Context, facts domain.SessionStorageFacts) error
	RecordSessionRestore(ctx context.Context, id domain.SessionID, at time.Time, restoreErr string) error
	InsertStorageActions(ctx context.Context, actions []domain.StorageAction) error
	ListStorageActions(ctx context.Context, limit int) ([]domain.StorageAction, error)
	PruneStorageActionsBefore(ctx context.Context, before time.Time) (int64, error)
	ListSessionEndTimes(ctx context.Context) (map[domain.SessionID]time.Time, error)
	DatabasePageStats(ctx context.Context) (pageSize, pageCount, freePages int64, err error)
	DatabaseTableSizes(ctx context.Context, limit int) ([]domain.StorageTableSize, bool, error)
	IncrementalVacuum(ctx context.Context, pages int64) error
}

// Sessions lists sessions and reclaims ended ones' worktrees through the
// session service's no-force cleanup.
type Sessions interface {
	ListAll(ctx context.Context) ([]domain.Session, error)
	CleanupSessions(ctx context.Context, ids []domain.SessionID) (cleaned []domain.SessionID, skipped map[domain.SessionID]string, err error)
}

// QuietGuard runs work while a session is provably quiet and locked against
// every other session operation (sessionmanager.Manager.WithQuietSession).
type QuietGuard interface {
	WithQuietSession(ctx context.Context, id domain.SessionID, fn func(domain.SessionRecord) error) error
}

// WorkspaceGuard runs work while no session can claim a worktree path
// (sessionmanager.Manager.WithUnownedWorkspace).
type WorkspaceGuard interface {
	WithUnownedWorkspace(ctx context.Context, projectID domain.ProjectID, path string, fn func() error) error
}

// Projects resolves project records.
type Projects interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
}

// Options configures a Service.
type Options struct {
	Layout   Layout
	Store    Store
	Sessions Sessions
	Quiet    QuietGuard
	Projects Projects
	Logger   *slog.Logger
	Now      func() time.Time
	// AutoCleanAllowed is the deployment kill switch (AO_STORAGE_AUTOCLEAN=off
	// clears it): off, scheduled passes only log what they would do.
	AutoCleanAllowed bool
	// Workspaces gates removing a worktree no session owns; nil skips orphans.
	Workspaces WorkspaceGuard
}

// Errors the HTTP layer maps to responses.
var (
	ErrScanRequired    = apierr.Conflict("STORAGE_SCAN_REQUIRED", "Storage has not been measured yet; a scan has started, try again when it finishes.", nil)
	ErrCleanInProgress = apierr.Conflict("STORAGE_CLEAN_IN_PROGRESS", "A storage cleanup is already running.", nil)
)

// staleSnapshot is how old a scan may be before a scheduled pass rescans.
const staleSnapshot = time.Hour

// auditRetention is how long the storage audit log is kept.
const auditRetention = 90 * day

// Service measures, plans and cleans AO's storage.
type Service struct {
	layout      Layout
	store       Store
	sessions    Sessions
	quiet       QuietGuard
	projects    Projects
	logger      *slog.Logger
	now         func() time.Time
	autoAllowed bool
	pressure    *pressureTracker
	workspaces  WorkspaceGuard

	// settingsMu makes a settings change one read-modify-write: the daemon
	// is the only writer, so two requests can never interleave.
	settingsMu sync.Mutex

	mu       sync.Mutex
	bg       context.Context
	snapshot *scanSnapshot
	scanning bool
	scanErr  string
	scanDone chan struct{}
	job      *cleanJob
	lastJob  *cleanJob
	wake     chan struct{}

	nodeMu           sync.Mutex
	nodeVersionValue string
	nodeCheckedAt    time.Time

	cloneOnce sync.Once
	cloneOK   bool
}

var _ sessionmanager.StorageHooks = (*Service)(nil)

// New builds the service. Call Start to run its schedule.
func New(opts Options) *Service {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{
		layout:      opts.Layout,
		store:       opts.Store,
		sessions:    opts.Sessions,
		quiet:       opts.Quiet,
		projects:    opts.Projects,
		logger:      logger,
		now:         now,
		autoAllowed: opts.AutoCleanAllowed,
		pressure:    newPressureTracker(),
		workspaces:  opts.Workspaces,
		bg:          context.Background(),
		wake:        make(chan struct{}, 1),
	}
}

// Report is what the storage endpoint and CLI show.
type Report struct {
	ScannedAt     time.Time
	ScanDuration  time.Duration
	Scanning      bool
	ScanError     string
	TotalBytes    int64
	Disk          DiskStatus
	Categories    []CategoryUsage
	Worktrees     []WorktreeUsage
	Sessions      map[domain.SessionID]SessionStorage
	Database      DatabaseUsage
	Plan          []PlannedAction
	Settings      domain.StorageSettings
	AutoAllowed   bool
	CloneSupport  bool
	Cleaning      bool
	LastClean     *CleanResult
	ReclaimByKind map[domain.StorageCleanKind]int64
}

// SessionStorage is a session's offload state for the report.
type SessionStorage struct {
	Offloaded      bool
	OffloadedAt    time.Time
	OffloadedBytes int64
	RestoreError   string
}

// categoryOrder is the report's category order.
var categoryOrder = []domain.StorageCategory{
	domain.StorageWorktrees, domain.StorageDatabase, domain.StorageDBBackups, domain.StorageHarnesses,
	domain.StorageBrowserData, domain.StorageLogs, domain.StorageDevProfiles, domain.StorageDependencyTemplates,
	domain.StorageOther,
}

// kindCategory maps a cleanup kind to the category whose space it frees.
var kindCategory = map[domain.StorageCleanKind]domain.StorageCategory{
	domain.StorageCleanOffload:         domain.StorageWorktrees,
	domain.StorageCleanEndedWorktrees:  domain.StorageWorktrees,
	domain.StorageCleanOrphanWorktrees: domain.StorageWorktrees,
	domain.StorageCleanBackups:         domain.StorageDBBackups,
	domain.StorageCleanLogs:            domain.StorageLogs,
	domain.StorageCleanDevProfiles:     domain.StorageDevProfiles,
	domain.StorageCleanTemplates:       domain.StorageDependencyTemplates,
	domain.StorageCleanDatabase:        domain.StorageDatabase,
	domain.StorageCleanTrash:           domain.StorageOther,
}

// Report returns the latest measurements with a fresh plan. It never waits on
// a scan; with none yet it starts one and reports Scanning.
func (s *Service) Report(ctx context.Context) (Report, error) {
	settings, err := s.store.GetStorageSettings(ctx)
	if err != nil {
		return Report{}, err
	}
	s.mu.Lock()
	snap, scanning, scanErr := s.snapshot, s.scanning, s.scanErr
	cleaning := s.job != nil
	var last *CleanResult
	if s.lastJob != nil {
		result := s.lastJob.result()
		last = &result
	}
	s.mu.Unlock()
	if snap == nil && !scanning {
		s.StartScan()
		scanning = true
	}
	disk := s.pressure.status()
	if disk.CheckedAt.IsZero() {
		disk = s.CheckPressure(ctx)
	}
	report := Report{
		Scanning: scanning, ScanError: scanErr, Disk: disk, Settings: settings,
		AutoAllowed: s.autoAllowed, CloneSupport: s.cloneSupported(), Cleaning: cleaning, LastClean: last,
		Sessions: map[domain.SessionID]SessionStorage{}, ReclaimByKind: map[domain.StorageCleanKind]int64{},
	}
	if snap == nil {
		return report, nil
	}
	report.ScannedAt, report.ScanDuration = snap.At, snap.Duration
	report.Worktrees, report.Database = snap.Worktrees, snap.Database
	report.Plan = plan(policyInput{Now: s.now(), Settings: settings, Pressure: disk.Level, Snapshot: snap})
	reclaim := map[domain.StorageCategory]int64{}
	for _, action := range report.Plan {
		report.ReclaimByKind[action.Kind] += action.Bytes
		reclaim[kindCategory[action.Kind]] += action.Bytes
	}
	for _, category := range categoryOrder {
		bytes := snap.Categories[category]
		report.TotalBytes += bytes
		report.Categories = append(report.Categories, CategoryUsage{Category: category, Bytes: bytes, ReclaimableBytes: min(reclaim[category], bytes)})
	}
	for _, sess := range snap.Sessions {
		if sess.Offloaded || sess.RestoreError != "" {
			report.Sessions[sess.ID] = SessionStorage{Offloaded: sess.Offloaded, OffloadedAt: sess.OffloadedAt, OffloadedBytes: sess.OffloadedBytes, RestoreError: sess.RestoreError}
		}
	}
	return report, nil
}

// StartScan begins a background scan unless one is running.
func (s *Service) StartScan() {
	s.mu.Lock()
	if s.scanning {
		s.mu.Unlock()
		return
	}
	s.scanning = true
	done := make(chan struct{})
	s.scanDone = done
	ctx := s.bg
	s.mu.Unlock()
	go func() {
		defer sentryobs.RecoverBackground()
		defer close(done)
		s.runScan(ctx)
	}()
}

// WaitScan blocks until the running scan (if any) finishes.
func (s *Service) WaitScan(ctx context.Context) error {
	s.mu.Lock()
	done := s.scanDone
	scanning := s.scanning
	s.mu.Unlock()
	if !scanning || done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) runScan(ctx context.Context) {
	snap, err := s.scan(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scanning = false
	if err != nil {
		s.scanErr = err.Error()
		s.logger.Warn("storage: scan failed", "error", err)
		return
	}
	s.snapshot, s.scanErr = snap, ""
	s.logger.Debug("storage: scan finished", "duration", snap.Duration)
}

// scanNow runs a scan and waits for it, sharing a running one. A failed scan
// keeps the previous snapshot, so it is reported as an error: nothing may act
// on a plan the failed scan was meant to refresh.
func (s *Service) scanNow(ctx context.Context) error {
	s.StartScan()
	if err := s.WaitScan(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scanErr != "" {
		return errors.New(s.scanErr)
	}
	return nil
}

// CheckPressure reads free space on the data volume now.
func (s *Service) CheckPressure(_ context.Context) DiskStatus {
	total, free, err := diskSpace(s.layout.DataDir)
	status, worsened := s.pressure.observe(s.layout.DataDir, clampUint(total), clampUint(free), err, s.now())
	if worsened {
		s.logger.Warn("storage: disk space is running out", "level", status.Level, "freeBytes", status.FreeBytes)
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
	return status
}

// Settings returns the storage preferences.
func (s *Service) Settings(ctx context.Context) (domain.StorageSettings, error) {
	return s.store.GetStorageSettings(ctx)
}

// UpdateSettings applies change to the current preferences, validates and
// saves them as one step, so two concurrent changes to different settings
// never overwrite each other.
func (s *Service) UpdateSettings(ctx context.Context, change func(*domain.StorageSettings)) (domain.StorageSettings, error) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	settings, err := s.store.GetStorageSettings(ctx)
	if err != nil {
		return domain.StorageSettings{}, err
	}
	change(&settings)
	if err := settings.Validate(); err != nil {
		return domain.StorageSettings{}, apierr.Invalid("INVALID_STORAGE_SETTINGS", err.Error(), nil)
	}
	if err := s.store.SaveStorageSettings(ctx, settings, s.now()); err != nil {
		return domain.StorageSettings{}, err
	}
	return s.store.GetStorageSettings(ctx)
}

// Actions returns the newest audit-log entries.
func (s *Service) Actions(ctx context.Context, limit int) ([]domain.StorageAction, error) {
	return s.store.ListStorageActions(ctx, limit)
}

// CleanRequest selects which planned actions a cleanup runs.
type CleanRequest struct {
	// Kinds limits the cleanup to these kinds; empty means every kind except
	// dev profiles (see IncludeDev).
	Kinds []domain.StorageCleanKind
	// SessionIDs limits session-scoped kinds to these sessions; any other kind
	// is left out when set.
	SessionIDs []domain.SessionID
	// DryRun returns the plan without changing anything.
	DryRun bool
	// IncludeDev adds dev-build profiles to a request without explicit kinds.
	IncludeDev bool
	// AutomaticOnly keeps only the actions the policy runs on its own.
	AutomaticOnly bool
}

// CleanResult reports a cleanup.
type CleanResult struct {
	StartedAt  time.Time
	FinishedAt time.Time
	Running    bool
	DryRun     bool
	Actions    []domain.StorageAction
	FreedBytes int64
}

type cleanJob struct {
	mu      sync.Mutex
	started time.Time
	done    time.Time
	actions []domain.StorageAction
	freed   int64
}

func (j *cleanJob) result() CleanResult {
	j.mu.Lock()
	defer j.mu.Unlock()
	return CleanResult{
		StartedAt: j.started, FinishedAt: j.done, Running: j.done.IsZero(),
		Actions: append([]domain.StorageAction(nil), j.actions...), FreedBytes: j.freed,
	}
}

func (j *cleanJob) add(action domain.StorageAction) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.actions = append(j.actions, action)
	if action.Result == domain.StorageActionDone {
		j.freed += action.Bytes
	}
}

func selectActions(planned []PlannedAction, req CleanRequest) []PlannedAction {
	kinds := map[domain.StorageCleanKind]bool{}
	for _, kind := range req.Kinds {
		kinds[kind] = true
	}
	sessions := map[domain.SessionID]bool{}
	for _, id := range req.SessionIDs {
		sessions[id] = true
	}
	out := make([]PlannedAction, 0, len(planned))
	for _, action := range planned {
		switch {
		case len(kinds) > 0 && !kinds[action.Kind]:
			continue
		case req.AutomaticOnly && !action.Automatic:
			continue
		case len(kinds) == 0 && action.Kind == domain.StorageCleanDevProfiles && !req.IncludeDev && !action.Automatic:
			continue
		case len(sessions) > 0 && !sessions[action.SessionID]:
			continue
		}
		out = append(out, action)
	}
	return out
}

// Clean plans a cleanup and, unless it is a dry run, starts it in the
// background. Only one cleanup runs at a time.
func (s *Service) Clean(ctx context.Context, req CleanRequest) (CleanResult, error) {
	for _, kind := range req.Kinds {
		if _, ok := domain.ParseStorageCleanKind(string(kind)); !ok {
			return CleanResult{}, apierr.Invalid("INVALID_STORAGE_KIND", fmt.Sprintf("Unknown cleanup kind %q", kind), nil)
		}
	}
	settings, err := s.store.GetStorageSettings(ctx)
	if err != nil {
		return CleanResult{}, err
	}
	s.mu.Lock()
	snap := s.snapshot
	s.mu.Unlock()
	if snap == nil {
		s.StartScan()
		return CleanResult{}, ErrScanRequired
	}
	disk := s.pressure.status()
	selected := selectActions(plan(policyInput{Now: s.now(), Settings: settings, Pressure: disk.Level, Snapshot: snap}), req)
	now := s.now()
	if req.DryRun {
		result := CleanResult{StartedAt: now, FinishedAt: now, DryRun: true, Actions: make([]domain.StorageAction, 0, len(selected))}
		for _, action := range selected {
			result.Actions = append(result.Actions, plannedRecord(action, now, true))
			result.FreedBytes += action.Bytes
		}
		return result, nil
	}
	// The response is built before the executor starts, which reorders its
	// own copy of the actions.
	planned := CleanResult{StartedAt: now, Running: true, Actions: make([]domain.StorageAction, 0, len(selected))}
	for _, action := range selected {
		planned.Actions = append(planned.Actions, plannedRecord(action, now, false))
		planned.FreedBytes += action.Bytes
	}
	job, err := s.beginJob()
	if err != nil {
		return CleanResult{}, err
	}
	actions := append([]PlannedAction(nil), selected...)
	go func() {
		defer sentryobs.RecoverBackground()
		s.execute(s.bg, job, actions)
	}()
	return planned, nil
}

func plannedRecord(action PlannedAction, at time.Time, dryRun bool) domain.StorageAction {
	return domain.StorageAction{
		At: at, Kind: action.Kind, Target: action.Target, SessionID: action.SessionID, Reason: action.Reason,
		Bytes: action.Bytes, DryRun: dryRun, Result: domain.StorageActionPlanned,
	}
}

func (s *Service) beginJob() (*cleanJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job != nil {
		return nil, ErrCleanInProgress
	}
	s.job = &cleanJob{started: s.now()}
	return s.job, nil
}

func (s *Service) endJob(job *cleanJob) {
	job.mu.Lock()
	job.done = s.now()
	job.mu.Unlock()
	s.mu.Lock()
	s.job, s.lastJob = nil, job
	s.mu.Unlock()
	// What is on disk changed; measure again.
	s.StartScan()
}

// execute runs the selected actions in a safe order: offloads and removals
// first, then the trash they filled, then the database.
func (s *Service) execute(ctx context.Context, job *cleanJob, actions []PlannedAction) {
	defer s.endJob(job)
	order := map[domain.StorageCleanKind]int{
		domain.StorageCleanOffload: 0, domain.StorageCleanEndedWorktrees: 1, domain.StorageCleanOrphanWorktrees: 2,
		domain.StorageCleanBackups: 3, domain.StorageCleanLogs: 3, domain.StorageCleanDevProfiles: 3,
		domain.StorageCleanTemplates: 3, domain.StorageCleanTrash: 4, domain.StorageCleanDatabase: 5,
	}
	sort.SliceStable(actions, func(i, j int) bool { return order[actions[i].Kind] < order[actions[j].Kind] })

	var ended []PlannedAction
	for _, action := range actions {
		if ctx.Err() != nil {
			return
		}
		switch action.Kind {
		case domain.StorageCleanEndedWorktrees:
			ended = append(ended, action)
			continue
		case domain.StorageCleanTrash:
			if len(ended) > 0 {
				s.finish(ctx, job, s.cleanEnded(ctx, ended)...)
				ended = nil
			}
		}
		s.finish(ctx, job, s.runAction(ctx, action))
	}
	if len(ended) > 0 {
		s.finish(ctx, job, s.cleanEnded(ctx, ended)...)
	}
	// Whatever the actions moved aside is deleted now, not on the next pass.
	s.emptyTrash(ctx)
}

func (s *Service) finish(ctx context.Context, job *cleanJob, actions ...domain.StorageAction) {
	for _, action := range actions {
		job.add(action)
		s.audit(ctx, action)
	}
}

func (s *Service) audit(ctx context.Context, actions ...domain.StorageAction) {
	if err := s.store.InsertStorageActions(context.WithoutCancel(ctx), actions); err != nil {
		s.logger.Warn("storage: write audit log", "error", err)
	}
}

func (s *Service) record(action PlannedAction, result domain.StorageActionResult, err error) domain.StorageAction {
	out := plannedRecord(action, s.now(), false)
	out.Result = result
	if err != nil {
		out.Error = err.Error()
	}
	if result != domain.StorageActionDone {
		out.Bytes = 0
	}
	return out
}

func (s *Service) runAction(ctx context.Context, action PlannedAction) domain.StorageAction {
	switch action.Kind {
	case domain.StorageCleanOffload:
		return s.runOffload(ctx, action)
	case domain.StorageCleanOrphanWorktrees:
		return s.runOrphanRemoval(ctx, action)
	case domain.StorageCleanBackups:
		return s.runRemoval(action, s.layout.DataDir)
	case domain.StorageCleanLogs:
		root := s.layout.Logs()
		if filepath.Dir(action.Target) != root {
			root = filepath.Dir(action.Target) // a loose log beside the state or data dir
			if root != s.layout.StateDir && root != s.layout.DataDir {
				return s.record(action, domain.StorageActionSkipped, errors.New("log is outside AO's folders"))
			}
		}
		return s.runRemoval(action, root)
	case domain.StorageCleanDevProfiles:
		if s.devProfileRunning(action.Target) {
			return s.record(action, domain.StorageActionSkipped, errors.New("this dev build is running"))
		}
		if action.Automatic && !s.devProfileStillExpired(ctx, action.Target) {
			// The plan came from an earlier scan; a profile used since then is
			// no longer due.
			return s.record(action, domain.StorageActionSkipped, errors.New("used again since it was measured"))
		}
		return s.runRemoval(action, s.layout.DevProfiles())
	case domain.StorageCleanTemplates:
		return s.runRemoval(action, s.layout.Templates())
	case domain.StorageCleanTrash:
		if !Within(s.layout.Trash(), action.Target) {
			return s.record(action, domain.StorageActionSkipped, errors.New("outside the storage trash"))
		}
		if err := removeTree(action.Target); err != nil {
			return s.record(action, domain.StorageActionFailed, err)
		}
		return s.record(action, domain.StorageActionDone, nil)
	case domain.StorageCleanDatabase:
		return s.runVacuum(ctx, action)
	default:
		return s.record(action, domain.StorageActionSkipped, fmt.Errorf("unknown cleanup kind %q", action.Kind))
	}
}

func (s *Service) runOffload(ctx context.Context, action PlannedAction) domain.StorageAction {
	var result offloadResult
	err := s.quiet.WithQuietSession(ctx, action.SessionID, func(rec domain.SessionRecord) error {
		if normalizePath(rec.Metadata.WorkspacePath) != normalizePath(action.Target) {
			return errors.New("the session's worktree moved since it was measured")
		}
		project, ok, err := s.projects.GetProject(ctx, string(rec.ProjectID))
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("project not found")
		}
		if project.Config.StorageOffloadDisabled {
			return errors.New("offload is turned off for this project")
		}
		prev, hadPrev, err := s.store.GetSessionStorageFacts(ctx, rec.ID)
		if err != nil {
			return err
		}
		offloadedAt := s.now()
		record := func(facts domain.SessionStorageFacts) error {
			if hadPrev && prev.Offloaded() {
				// An earlier offload's paths are still missing; keep them so a
				// resume restores those too.
				facts = mergeOffloadFacts(prev, facts)
			}
			facts.SessionID, facts.OffloadedAt = rec.ID, offloadedAt
			return s.store.RecordSessionOffload(ctx, facts)
		}
		offloaded, offloadErr := s.offloadWorktree(ctx, project, rec.Metadata.WorkspacePath, record)
		result = offloaded
		return offloadErr
	})
	switch {
	case errors.Is(err, sessionmanager.ErrSessionNotQuiet), errors.Is(err, sessionmanager.ErrOperationInProgress),
		errors.Is(err, sessionmanager.ErrTerminated), errors.Is(err, sessionmanager.ErrNotFound):
		return s.record(action, domain.StorageActionSkipped, err)
	case err != nil && len(result.facts.OffloadedPaths) == 0:
		return s.record(action, domain.StorageActionFailed, err)
	}
	out := s.record(action, domain.StorageActionDone, err)
	// Only trashed trees went back to the disk; one adopted as a dependency
	// template is still on it.
	out.Bytes = result.freed
	return out
}

// mergeOffloadFacts combines an unsettled earlier offload with a new one. A
// path the earlier offload removed is still missing, so the new run cannot
// have found it again; the byte totals therefore add up.
func mergeOffloadFacts(prev, next domain.SessionStorageFacts) domain.SessionStorageFacts {
	merged := domain.SessionStorageFacts{
		OffloadedPaths: append([]string(nil), next.OffloadedPaths...),
		OffloadedBytes: next.OffloadedBytes + prev.OffloadedBytes,
		TemplateKeys:   map[string]string{},
	}
	seen := map[string]bool{}
	for _, p := range next.OffloadedPaths {
		seen[p] = true
	}
	for _, p := range prev.OffloadedPaths {
		if !seen[p] {
			merged.OffloadedPaths = append(merged.OffloadedPaths, p)
		}
	}
	for k, v := range prev.TemplateKeys {
		merged.TemplateKeys[k] = v
	}
	for k, v := range next.TemplateKeys {
		merged.TemplateKeys[k] = v
	}
	return merged
}

func (s *Service) cleanEnded(ctx context.Context, actions []PlannedAction) []domain.StorageAction {
	ids := make([]domain.SessionID, 0, len(actions))
	for _, action := range actions {
		ids = append(ids, action.SessionID)
	}
	cleaned, skipped, err := s.sessions.CleanupSessions(ctx, ids)
	done := map[domain.SessionID]bool{}
	for _, id := range cleaned {
		done[id] = true
	}
	out := make([]domain.StorageAction, 0, len(actions))
	for _, action := range actions {
		switch {
		case done[action.SessionID]:
			out = append(out, s.record(action, domain.StorageActionDone, nil))
		case skipped[action.SessionID] != "":
			out = append(out, s.record(action, domain.StorageActionSkipped, errors.New(skipped[action.SessionID])))
		case err != nil:
			out = append(out, s.record(action, domain.StorageActionFailed, err))
		default:
			out = append(out, s.record(action, domain.StorageActionSkipped, errors.New("already released")))
		}
	}
	return out
}

func (s *Service) runOrphanRemoval(ctx context.Context, action PlannedAction) domain.StorageAction {
	if !Within(s.layout.Worktrees(), action.Target) {
		return s.record(action, domain.StorageActionSkipped, errors.New("outside AO's managed worktrees"))
	}
	if s.workspaces == nil {
		return s.record(action, domain.StorageActionSkipped, errors.New("worktree ownership cannot be checked"))
	}
	errDirty := errors.New("it has uncommitted changes")
	// Ownership is checked again, and the worktree removed, under the project's
	// workspace gate: no spawn, restore or import can claim it in between.
	err := s.workspaces.WithUnownedWorkspace(ctx, action.ProjectID, action.Target, func() error {
		status, err := gitCommand(ctx, action.Target, "status", "--porcelain=v1", "--untracked-files=normal")
		if err != nil {
			return err
		}
		if len(status) > 0 {
			return errDirty
		}
		common, err := gitCommand(ctx, action.Target, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil {
			return err
		}
		// Plain `worktree remove` (no --force) refuses a dirty or locked worktree.
		_, err = gitCommand(ctx, strings.TrimSpace(string(common)), "worktree", "remove", action.Target)
		return err
	})
	switch {
	case errors.Is(err, errDirty), errors.Is(err, sessionmanager.ErrWorkspaceOwned):
		return s.record(action, domain.StorageActionSkipped, err)
	case err != nil:
		return s.record(action, domain.StorageActionFailed, err)
	}
	return s.record(action, domain.StorageActionDone, nil)
}

// runRemoval deletes a file or tree that must sit inside root: moved aside
// first when it can be, deleted in place otherwise.
func (s *Service) runRemoval(action PlannedAction, root string) domain.StorageAction {
	if !Within(root, action.Target) {
		return s.record(action, domain.StorageActionSkipped, errors.New("outside AO's folders"))
	}
	if !pathExists(action.Target) {
		return s.record(action, domain.StorageActionSkipped, errors.New("already gone"))
	}
	if moved, err := moveToTrash(s.layout.Trash(), action.Target); err == nil {
		out := s.record(action, domain.StorageActionDone, nil)
		if err := removeTree(moved); err != nil {
			// Still on the disk until a later pass empties the trash, which
			// then counts it.
			s.logger.Debug("storage: trash delete deferred", "path", moved, "error", err)
			out.Bytes = 0
			out.Reason += "; the delete finishes on a later pass"
		}
		return out
	}
	if err := removeTree(action.Target); err != nil {
		return s.record(action, domain.StorageActionFailed, err)
	}
	return s.record(action, domain.StorageActionDone, nil)
}

func (s *Service) runVacuum(ctx context.Context, action PlannedAction) domain.StorageAction {
	pageSize, _, before, err := s.store.DatabasePageStats(ctx)
	if err != nil {
		return s.record(action, domain.StorageActionFailed, err)
	}
	// Small steps keep each write lock short; a busy daemon interleaves.
	for range 200 {
		if err := ctx.Err(); err != nil {
			return s.record(action, domain.StorageActionFailed, err)
		}
		_, _, free, statErr := s.store.DatabasePageStats(ctx)
		if statErr != nil {
			return s.record(action, domain.StorageActionFailed, statErr)
		}
		if free == 0 {
			break
		}
		if err := s.store.IncrementalVacuum(ctx, 1000); err != nil {
			return s.record(action, domain.StorageActionFailed, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	out := s.record(action, domain.StorageActionDone, nil)
	_, _, after, err := s.store.DatabasePageStats(ctx)
	if err != nil {
		out.Bytes = 0
		out.Error = "could not measure what was freed: " + err.Error()
		return out
	}
	out.Bytes = max(before-after, 0) * pageSize
	return out
}

// devProfileStillExpired re-reads a dev profile's last use against the
// current setting, so a scheduled removal never acts on an outdated scan.
func (s *Service) devProfileStillExpired(ctx context.Context, dir string) bool {
	settings, err := s.store.GetStorageSettings(ctx)
	if err != nil {
		return false
	}
	info, err := os.Stat(dir)
	if err != nil {
		return false
	}
	return s.now().Sub(devProfileLastUsed(dir, info.ModTime())) >= time.Duration(settings.DevProfileDays)*day
}

func (s *Service) emptyTrash(ctx context.Context) {
	entries, err := listEntries(ctx, s.layout.Trash(), nil)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if err := removeTree(entry.Path); err != nil {
			s.logger.Debug("storage: trash delete deferred", "path", entry.Path, "error", err)
		}
	}
}

func (s *Service) devProfileRunning(dir string) bool {
	info, err := runfile.Read(filepath.Join(dir, "running.json"))
	if err != nil {
		// Unreadable: assume it may be running.
		return true
	}
	return info != nil && info.PID > 0 && processalive.Alive(info.PID)
}

// Start runs the schedule until ctx ends: a free-space check every few
// minutes (every minute under pressure) and an automatic pass a few minutes
// after boot, then daily, sooner under pressure or when space suddenly drops.
func (s *Service) Start(ctx context.Context) <-chan struct{} {
	s.mu.Lock()
	s.bg = ctx
	s.mu.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer sentryobs.RecoverBackground()
		nextPressure := s.now()
		nextPass := s.now().Add(3 * time.Minute)
		for {
			now := s.now()
			if !now.Before(nextPressure) {
				status := s.CheckPressure(ctx)
				nextPressure = now.Add(pressureCheckInterval(status.Level))
			}
			if !now.Before(nextPass) {
				s.automaticPass(ctx)
				nextPass = s.now().Add(passInterval(s.pressure.status().Level))
			}
			wait := min(nextPressure.Sub(s.now()), nextPass.Sub(s.now()))
			timer := time.NewTimer(max(wait, time.Second))
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-s.wake:
				timer.Stop()
				nextPass = s.now()
			case <-timer.C:
			}
		}
	}()
	return done
}

func passInterval(level domain.DiskPressureLevel) time.Duration {
	switch level {
	case domain.DiskPressureCritical:
		return time.Hour
	case domain.DiskPressureLow:
		return 2 * time.Hour
	default:
		return day
	}
}

// automaticPass runs what the policy runs on its own. With auto-clean off (by
// setting or kill switch) it records the would-be actions as a dry run, so
// people can see what turning it on would do.
func (s *Service) automaticPass(ctx context.Context) {
	s.mu.Lock()
	snap := s.snapshot
	s.mu.Unlock()
	if snap == nil || s.now().Sub(snap.At) > staleSnapshot {
		if err := s.scanNow(ctx); err != nil {
			return
		}
		s.mu.Lock()
		snap = s.snapshot
		s.mu.Unlock()
		if snap == nil {
			return
		}
	}
	settings, err := s.store.GetStorageSettings(ctx)
	if err != nil {
		s.logger.Warn("storage: read settings", "error", err)
		return
	}
	if _, err := s.store.PruneStorageActionsBefore(ctx, s.now().Add(-auditRetention)); err != nil {
		s.logger.Debug("storage: prune audit log", "error", err)
	}
	selected := selectActions(plan(policyInput{Now: s.now(), Settings: settings, Pressure: s.pressure.status().Level, Snapshot: snap}),
		CleanRequest{AutomaticOnly: true})
	if len(selected) == 0 {
		return
	}
	if !s.autoAllowed || !settings.AutoClean {
		now := s.now()
		shadow := make([]domain.StorageAction, 0, len(selected))
		for _, action := range selected {
			if action.Kind == domain.StorageCleanTrash {
				continue
			}
			shadow = append(shadow, plannedRecord(action, now, true))
		}
		s.audit(ctx, shadow...)
		return
	}
	job, err := s.beginJob()
	if err != nil {
		return // a person's cleanup is running; the next pass catches up
	}
	s.execute(ctx, job, selected)
}
