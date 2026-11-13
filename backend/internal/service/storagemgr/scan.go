package storagemgr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// WorktreeState is how a worktree relates to its session.
type WorktreeState string

// Worktree states.
const (
	// WorktreeLive belongs to an open session whose agent may be running.
	WorktreeLive WorktreeState = "live"
	// WorktreeQuiet belongs to an open session whose agent exited or hibernated.
	WorktreeQuiet WorktreeState = "quiet"
	// WorktreeEnded belongs to a terminated session.
	WorktreeEnded WorktreeState = "ended"
	// WorktreeOrphan is under the managed root but owned by no session.
	WorktreeOrphan WorktreeState = "orphan"
)

// WorktreeUsage is one worktree folder under the managed root.
type WorktreeUsage struct {
	Path      string
	ProjectID domain.ProjectID
	SessionID domain.SessionID
	State     WorktreeState
	Bytes     int64
	// DependencyBytes is what an offload would free right now.
	DependencyBytes int64
	// GitWorktree reports a folder that is a registered git worktree.
	GitWorktree bool
	// Clean reports an orphan git worktree with no uncommitted changes.
	Clean bool
	// LastUsed is the session's last activity, or the folder's mtime for an
	// orphan.
	LastUsed time.Time
}

// CategoryUsage is how much one category holds and could free.
type CategoryUsage struct {
	Category         domain.StorageCategory
	Bytes            int64
	ReclaimableBytes int64
}

// DatabaseUsage sizes the database files.
type DatabaseUsage struct {
	FileBytes   int64
	WALBytes    int64
	FreeBytes   int64
	Tables      []domain.StorageTableSize
	TablesKnown bool
}

// scanSnapshot is everything one scan measured. Plans are computed from it on
// demand, so a settings change applies without rescanning.
type scanSnapshot struct {
	At          time.Time
	Duration    time.Duration
	Worktrees   []WorktreeUsage
	Database    DatabaseUsage
	Backups     []fileEntry
	Logs        []fileEntry
	DevProfiles []devProfile
	Templates   []templateEntry
	Trash       []fileEntry
	Categories  map[domain.StorageCategory]int64
	Sessions    []sessionView
}

type devProfile struct {
	fileEntry
	Running bool
}

type templateEntry struct {
	fileEntry
	Key      string
	LastUsed time.Time
}

// sessionView is the slice of a session the policy needs.
type sessionView struct {
	ID              domain.SessionID
	ProjectID       domain.ProjectID
	Orchestrator    bool
	Terminated      bool
	Quiet           bool
	WorkspacePath   string
	LastActivity    time.Time
	EndedAt         time.Time
	PRCount         int
	PRsDone         bool
	Cleanup         domain.WorkspaceDisposition
	Offloaded       bool
	OffloadedAt     time.Time
	OffloadedBytes  int64
	RestoreError    string
	OffloadDisabled bool
	WorkspaceKind   bool
}

// quietHint is the durable-fact reading of "agent not running"; the guard
// re-checks it against live process state before anything is removed.
func quietHint(s domain.Session) bool {
	if s.HibernatedAt != nil {
		return true
	}
	return s.Activity.State == domain.ActivityExited
}

func (s *Service) collectSessions(ctx context.Context) ([]sessionView, error) {
	sessions, err := s.sessions.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	facts, err := s.store.ListSessionStorageFacts(ctx)
	if err != nil {
		return nil, err
	}
	ended, err := s.store.ListSessionEndTimes(ctx)
	if err != nil {
		return nil, err
	}
	projects := map[domain.ProjectID]domain.ProjectRecord{}
	views := make([]sessionView, 0, len(sessions))
	for i := range sessions {
		sess := &sessions[i]
		project, ok := projects[sess.ProjectID]
		if !ok && sess.ProjectID != "" {
			if record, found, getErr := s.projects.GetProject(ctx, string(sess.ProjectID)); getErr == nil && found {
				project = record
			}
			projects[sess.ProjectID] = project
		}
		view := sessionView{
			ID:              sess.ID,
			ProjectID:       sess.ProjectID,
			Orchestrator:    sess.Kind == domain.KindOrchestrator,
			Terminated:      sess.IsTerminated,
			Quiet:           quietHint(*sess),
			WorkspacePath:   sess.Metadata.WorkspacePath,
			LastActivity:    sess.LastEventAt(),
			EndedAt:         ended[sess.ID],
			PRCount:         len(sess.PRs),
			Cleanup:         sess.WorkspaceCleanup,
			OffloadDisabled: project.Config.StorageOffloadDisabled,
			WorkspaceKind:   project.Kind.WithDefault() == domain.ProjectKindWorkspace,
		}
		view.PRsDone = view.PRCount > 0
		for _, pr := range sess.PRs {
			if !pr.Merged && !pr.Closed {
				view.PRsDone = false
			}
		}
		if f, ok := facts[sess.ID]; ok {
			view.Offloaded = f.Offloaded()
			view.OffloadedAt = f.OffloadedAt
			view.OffloadedBytes = f.OffloadedBytes
			view.RestoreError = f.RestoreError
		}
		views = append(views, view)
	}
	return views, nil
}

// scan measures everything under the state dir. It is slow on big worktrees
// and runs in the background; ctx cancels it.
func (s *Service) scan(ctx context.Context) (*scanSnapshot, error) {
	started := s.now()
	snap := &scanSnapshot{At: started, Categories: map[domain.StorageCategory]int64{}}
	sessions, err := s.collectSessions(ctx)
	if err != nil {
		return nil, err
	}
	snap.Sessions = sessions

	if snap.Worktrees, err = s.scanWorktrees(ctx, sessions); err != nil {
		return nil, err
	}
	for _, wt := range snap.Worktrees {
		snap.Categories[domain.StorageWorktrees] += wt.Bytes
	}

	snap.Database = s.scanDatabase(ctx)
	snap.Categories[domain.StorageDatabase] = snap.Database.FileBytes + snap.Database.WALBytes

	if snap.Backups, err = listEntries(ctx, s.layout.DataDir, func(name string, isDir bool) bool {
		return !isDir && (strings.HasPrefix(name, "ao.db.backup") || strings.HasPrefix(name, "ao.db.bak"))
	}); err != nil {
		return nil, err
	}
	for _, b := range snap.Backups {
		snap.Categories[domain.StorageDBBackups] += b.Bytes
	}

	var activeLogBytes int64
	if snap.Logs, activeLogBytes, err = s.scanLogs(ctx); err != nil {
		return nil, err
	}
	snap.Categories[domain.StorageLogs] = activeLogBytes
	for _, l := range snap.Logs {
		snap.Categories[domain.StorageLogs] += l.Bytes
	}

	if snap.DevProfiles, err = s.scanDevProfiles(ctx); err != nil {
		return nil, err
	}
	for _, p := range snap.DevProfiles {
		snap.Categories[domain.StorageDevProfiles] += p.Bytes
	}

	if snap.Templates, err = s.scanTemplates(ctx); err != nil {
		return nil, err
	}
	for _, t := range snap.Templates {
		snap.Categories[domain.StorageDependencyTemplates] += t.Bytes
	}

	if snap.Trash, err = listEntries(ctx, s.layout.Trash(), nil); err != nil {
		return nil, err
	}

	for category, dir := range map[domain.StorageCategory]string{
		domain.StorageHarnesses:   s.layout.Harnesses(),
		domain.StorageBrowserData: s.layout.Electron(),
	} {
		size, sizeErr := treeSize(ctx, dir)
		if sizeErr != nil {
			return nil, sizeErr
		}
		snap.Categories[category] = size
	}

	other, err := s.scanOther(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range snap.Trash {
		other += t.Bytes
	}
	snap.Categories[domain.StorageOther] = other
	snap.Duration = s.now().Sub(started)
	return snap, nil
}

func (s *Service) scanWorktrees(ctx context.Context, sessions []sessionView) ([]WorktreeUsage, error) {
	byPath := map[string]sessionView{}
	for _, sess := range sessions {
		if sess.WorkspacePath == "" {
			continue
		}
		key := normalizePath(sess.WorkspacePath)
		// A live successor can share a terminated predecessor's path; the live
		// one owns the folder.
		if existing, ok := byPath[key]; ok && !existing.Terminated {
			continue
		}
		byPath[key] = sess
	}
	projectDirs, err := os.ReadDir(s.layout.Worktrees())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []WorktreeUsage
	for _, projectDir := range projectDirs {
		if !projectDir.IsDir() {
			continue
		}
		root := filepath.Join(s.layout.Worktrees(), projectDir.Name())
		children, readErr := os.ReadDir(root)
		if readErr != nil {
			continue
		}
		for _, child := range children {
			if !child.IsDir() {
				continue
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			usage := s.measureWorktree(ctx, filepath.Join(root, child.Name()), byPath)
			if usage.ProjectID == "" {
				usage.ProjectID = domain.ProjectID(projectDir.Name())
			}
			out = append(out, usage)
		}
	}
	return out, nil
}

func (s *Service) measureWorktree(ctx context.Context, dir string, byPath map[string]sessionView) WorktreeUsage {
	usage := WorktreeUsage{Path: dir, State: WorktreeOrphan}
	usage.Bytes, _ = treeSize(ctx, dir)
	usage.GitWorktree = pathExists(filepath.Join(dir, ".git"))
	if sess, ok := byPath[normalizePath(dir)]; ok {
		usage.SessionID, usage.ProjectID, usage.LastUsed = sess.ID, sess.ProjectID, sess.LastActivity
		switch {
		case sess.Terminated:
			usage.State = WorktreeEnded
		case sess.Quiet:
			usage.State = WorktreeQuiet
		default:
			usage.State = WorktreeLive
		}
	} else if info, err := os.Stat(dir); err == nil {
		usage.LastUsed = info.ModTime()
	}
	if usage.GitWorktree && usage.State != WorktreeLive {
		if targets, err := findOffloadTargets(ctx, dir, s.projectOffloadPaths(ctx, usage.ProjectID)); err == nil {
			for _, t := range targets {
				usage.DependencyBytes += t.Bytes
			}
		}
	}
	if usage.State == WorktreeOrphan && usage.GitWorktree {
		if out, err := gitCommand(ctx, dir, "status", "--porcelain=v1", "--untracked-files=normal"); err == nil {
			usage.Clean = strings.TrimSpace(string(out)) == ""
		}
	}
	return usage
}

func (s *Service) projectOffloadPaths(ctx context.Context, id domain.ProjectID) []string {
	if id == "" {
		return nil
	}
	project, ok, err := s.projects.GetProject(ctx, string(id))
	if err != nil || !ok {
		return nil
	}
	return project.Config.StorageOffloadPaths
}

func (s *Service) scanDatabase(ctx context.Context) DatabaseUsage {
	var usage DatabaseUsage
	if info, err := os.Stat(s.layout.Database()); err == nil {
		usage.FileBytes = info.Size()
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if info, err := os.Stat(s.layout.Database() + suffix); err == nil {
			usage.WALBytes += info.Size()
		}
	}
	if pageSize, _, free, err := s.store.DatabasePageStats(ctx); err == nil {
		usage.FreeBytes = pageSize * free
	}
	if tables, ok, err := s.store.DatabaseTableSizes(ctx, 15); err == nil {
		usage.Tables, usage.TablesKnown = tables, ok
	}
	return usage
}

// scanLogs lists the log directory's files, which the policy may age out,
// and sizes the daemon's and hooks' own log files, which it never removes:
// they are live sinks that can stay unwritten for weeks on a healthy install.
func (s *Service) scanLogs(ctx context.Context) (logs []fileEntry, activeBytes int64, err error) {
	logs, err = listEntries(ctx, s.layout.Logs(), func(_ string, isDir bool) bool { return !isDir })
	if err != nil {
		return nil, 0, err
	}
	for _, loose := range s.layout.LooseLogFiles() {
		if info, statErr := os.Stat(loose); statErr == nil && info.Mode().IsRegular() {
			activeBytes += info.Size()
		}
	}
	return logs, activeBytes, nil
}

func (s *Service) scanDevProfiles(ctx context.Context) ([]devProfile, error) {
	if normalizePath(s.layout.DevProfiles()) == normalizePath(s.layout.DataDir) {
		return nil, nil
	}
	entries, err := listEntries(ctx, s.layout.DevProfiles(), func(_ string, isDir bool) bool { return isDir })
	if err != nil {
		return nil, err
	}
	out := make([]devProfile, 0, len(entries))
	for _, entry := range entries {
		profile := devProfile{fileEntry: entry, Running: s.devProfileRunning(entry.Path)}
		profile.ModTime = devProfileLastUsed(entry.Path, entry.ModTime)
		out = append(out, profile)
	}
	return out, nil
}

// devProfileLastUsed is when a dev profile last changed: the newest of its
// folder and the files a running dev build writes. The top folder's mtime
// alone misses writes deep in its database.
func devProfileLastUsed(dir string, folderMod time.Time) time.Time {
	last := folderMod
	for _, marker := range []string{"running.json", filepath.Join("data", "ao.db"), filepath.Join("data", "ao.db-wal")} {
		if info, err := os.Stat(filepath.Join(dir, marker)); err == nil && info.ModTime().After(last) {
			last = info.ModTime()
		}
	}
	return last
}

func (s *Service) scanTemplates(ctx context.Context) ([]templateEntry, error) {
	entries, err := listEntries(ctx, s.layout.Templates(), func(name string, isDir bool) bool { return isDir && !strings.HasPrefix(name, ".") })
	if err != nil {
		return nil, err
	}
	out := make([]templateEntry, 0, len(entries))
	for _, entry := range entries {
		key := filepath.Base(entry.Path)
		lastUsed := entry.ModTime
		if info, statErr := os.Stat(filepath.Join(entry.Path, templateUsedName)); statErr == nil {
			lastUsed = info.ModTime()
		}
		out = append(out, templateEntry{fileEntry: entry, Key: key, LastUsed: lastUsed})
	}
	return out, nil
}

// scanOther sizes everything under the data and state dirs that no category
// claims.
func (s *Service) scanOther(ctx context.Context) (int64, error) {
	claimed := map[string]bool{}
	for _, p := range []string{
		s.layout.Worktrees(), s.layout.Database(), s.layout.Database() + "-wal", s.layout.Database() + "-shm",
		s.layout.Harnesses(), s.layout.Templates(), s.layout.Trash(), s.layout.Electron(), s.layout.Logs(),
		s.layout.DevProfiles(), s.layout.DataDir,
	} {
		claimed[normalizePath(p)] = true
	}
	for _, p := range s.layout.LooseLogFiles() {
		claimed[normalizePath(p)] = true
	}
	var total int64
	roots := []string{s.layout.DataDir}
	// Under an AO_DATA_DIR override the state and data dirs are one folder.
	if normalizePath(s.layout.StateDir) != normalizePath(s.layout.DataDir) {
		roots = append(roots, s.layout.StateDir)
	}
	for _, dir := range roots {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			full := filepath.Join(dir, entry.Name())
			name := entry.Name()
			if claimed[normalizePath(full)] || strings.HasPrefix(name, "ao.db.backup") || strings.HasPrefix(name, "ao.db.bak") {
				continue
			}
			size, err := treeSize(ctx, full)
			if err != nil {
				return 0, err
			}
			total += size
		}
	}
	return total, nil
}

func normalizePath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	p = filepath.Clean(p)
	if filepath.Separator == '\\' {
		p = strings.ToLower(p)
	}
	return p
}
