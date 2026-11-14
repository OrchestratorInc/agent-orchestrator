package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type asyncChatSpawn struct {
	cfg               ports.SpawnConfig
	project           domain.ProjectRecord
	projectKind       domain.ProjectKind
	record            domain.SessionRecord
	branch            string
	prompt            string
	systemPrompt      string
	systemPromptFile  string
	promptBytes       int
	systemPromptBytes int
	preparation       *taskPreparation
	releaseHarness    func()
	retry             bool
}

type asyncChatSpawnRun struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (m *Manager) beginAsyncChatSpawn(ctx context.Context, in asyncChatSpawn) (domain.SessionRecord, int, int, error) {
	id := in.record.ID
	rollback := func() {
		if in.retry {
			return
		}
		if in.preparation == nil {
			m.rollbackSpawnSeedRowAfterFailure(ctx, id)
			return
		}
		cleanupCtx, cancel := spawnRollbackContext(ctx)
		m.discardClaimedTaskPreparation(cleanupCtx, in.preparation)
		cancel()
	}
	rec, err := m.setProvisionState(ctx, id, domain.SessionProvisionProvisioning, "")
	if err != nil {
		rollback()
		return domain.SessionRecord{}, 0, 0, wrapSpawnStage(id, ErrSpawnCreate, err)
	}
	if !in.retry && len(in.cfg.Attachments) > 0 {
		stageStarted := time.Now()
		for i, attachment := range in.cfg.Attachments {
			if err := m.attachments.PutCanonical(ctx, id, spawnAttachmentName(i, attachment), attachment.Data); err != nil {
				rollback()
				return domain.SessionRecord{}, 0, 0, wrapSpawnStage(id, ErrSpawnAttachments, err)
			}
		}
		m.logAsyncChatSpawnStage(id, "spawn_attachments", stageStarted)
	}
	if in.prompt != "" {
		if _, err := m.chat.QueueChatPrompt(ctx, id, in.prompt); err != nil {
			rollback()
			return domain.SessionRecord{}, 0, 0, wrapSpawnStage(id, ErrSpawnDeliverPrompt, err)
		}
		rec, err = m.getRecord(ctx, id)
		if err != nil {
			rollback()
			return domain.SessionRecord{}, 0, 0, err
		}
	}
	in.record = rec
	bg, cancel := context.WithTimeout(m.backgroundContext, postCreateBudget(asyncChatSpawnBudget, in.project.Config.PostCreate))
	bg = carrySpawnTrace(ctx, bg)
	NoteSpawnLaunch(ctx, domain.SessionModeChat, true)
	run := &asyncChatSpawnRun{cancel: cancel, done: make(chan struct{})}
	m.asyncChatSpawnsMu.Lock()
	m.asyncChatSpawns[id] = run
	m.asyncChatSpawnsMu.Unlock()
	m.runInBackground(func() {
		if in.retry {
			defer m.endAgentResume(id)
		}
		defer func() {
			if in.releaseHarness != nil {
				in.releaseHarness()
			}
			cancel()
			m.asyncChatSpawnsMu.Lock()
			if m.asyncChatSpawns[id] == run {
				delete(m.asyncChatSpawns, id)
			}
			close(run.done)
			m.asyncChatSpawnsMu.Unlock()
		}()
		m.completeAsyncChatSpawn(bg, in)
	})
	return rec, in.promptBytes, in.systemPromptBytes, nil
}

const asyncChatSpawnBudget = 10 * time.Minute

func (m *Manager) completeAsyncChatSpawn(ctx context.Context, in asyncChatSpawn) {
	id := in.record.ID
	totalStarted := time.Now()
	var taskName func() generatedTaskName
	cancelNaming := context.CancelFunc(func() {})
	if taskNamingApplies(in.cfg) {
		m.setBranchNaming(id, true)
		cancelNaming = func() { m.setBranchNaming(id, false) }
	}
	namingHandedOff := false
	defer func() {
		if !namingHandedOff {
			cancelNaming()
		}
	}()
	stageStarted := totalStarted
	var ws ports.WorkspaceInfo
	var workspaceProject *ports.WorkspaceProjectInfo
	var err error
	reusePublishedWorkspace := in.retry && in.record.Metadata.WorkspacePath != ""
	if reusePublishedWorkspace {
		if missing, _ := workspacePathMissing(in.record.Metadata.WorkspacePath); missing {
			reusePublishedWorkspace, in.record.ProvisionSteps = false, nil
		}
	}
	plan := provisionPlan(in, reusePublishedWorkspace)
	progress := m.startProvisionProgress(ctx, id, plan)
	if reusePublishedWorkspace {
		ws = workspaceInfo(in.record)
		if in.projectKind == domain.ProjectKindWorkspace {
			var rows []ports.WorkspaceRepoInfo
			rows, _, err = m.workspaceProjectRows(ctx, in.record)
			if err == nil {
				workspaceProject = &ports.WorkspaceProjectInfo{Root: ws, Worktrees: rows}
			}
		}
	}
	if in.preparation != nil {
		prepared := BeginSpawnStep(ctx, SpawnStepWorktree)
		ws, workspaceProject, err = m.awaitTaskPreparation(ctx, in.preparation)
		if err != nil && ctx.Err() != nil {
			prepared(err)
			in.preparation.cancel()
			m.failAsyncChatSpawn(ctx, id, wrapSpawnStage(id, ErrWorkspaceCreate, err))
			<-in.preparation.done
			if in.preparation.workspace.Path != "" {
				m.cleanupAsyncChatWorkspace(ctx, id, in.preparation.workspace, in.preparation.workspaceProject)
			}
			return
		}
		prepared(nil)
	}
	releaseWorkspaceGate := sync.OnceFunc(m.acquireWorkspaceGate(in.cfg.ProjectID))
	ctx = context.WithValue(ctx, spawnWorkspaceGateKey{}, releaseWorkspaceGate)
	defer releaseWorkspaceGate()
	if ws.Path == "" {
		fetched := BeginSpawnStep(ctx, SpawnStepBranchFetch)
		baseRefs := m.refreshDefaultBranchesBestEffort(ctx, in.project)
		fetched(nil)
		m.logAsyncChatSpawnStage(id, "default_branch_refresh", stageStarted)
		progress.complete(ctx, domain.SessionProvisionStepFetch)
		stageStarted = time.Now()
		created := BeginSpawnStep(ctx, SpawnStepWorktree)
		ws, workspaceProject, err = m.createSessionWorkspace(ctx, in.project, in.cfg, id, in.branch, baseRefs)
		created(err)
	} else {
		m.logAsyncChatSpawnStage(id, "prepared_workspace_wait", stageStarted)
		stageStarted = time.Now()
	}
	if err != nil {
		if ws.Path != "" {
			m.cleanupAsyncChatWorkspace(ctx, id, ws, workspaceProject)
		}
		m.failAsyncChatSpawn(ctx, id, wrapSpawnStage(id, ErrWorkspaceCreate, err))
		return
	}
	if err := ctx.Err(); err != nil {
		m.cleanupAsyncChatWorkspace(ctx, id, ws, workspaceProject)
		m.failAsyncChatSpawn(ctx, id, err)
		return
	}
	m.logAsyncChatSpawnStage(id, "workspace_create", stageStarted)
	progress.complete(ctx, domain.SessionProvisionStepWorktree)
	stageStarted = time.Now()
	if !reusePublishedWorkspace || slices.Contains(plan, domain.SessionProvisionStepSetup) {
		provisioned := BeginSpawnStep(ctx, SpawnStepSetup)
		err := m.provisionWorkspace(ctx, in.project, ws.Path)
		provisioned(err)
		if err != nil {
			m.cleanupAsyncChatWorkspace(ctx, id, ws, workspaceProject)
			m.failAsyncChatSpawn(ctx, id, wrapSpawnStage(id, ErrWorkspaceProvision, err))
			return
		}
	}
	if err := ctx.Err(); err != nil {
		m.cleanupAsyncChatWorkspace(ctx, id, ws, workspaceProject)
		m.failAsyncChatSpawn(ctx, id, err)
		return
	}
	m.logAsyncChatSpawnStage(id, "workspace_provision", stageStarted)
	progress.complete(ctx, domain.SessionProvisionStepSetup)
	stageStarted = time.Now()
	updated, err := m.store.SetSessionProvisionedWorkspace(
		ctx, id, ws.Branch, ws.Path, ws.RepoPath, m.clock())
	if err != nil {
		m.cleanupAsyncChatWorkspace(ctx, id, ws, workspaceProject)
		m.failAsyncChatSpawn(ctx, id, err)
		return
	}
	if !updated || ctx.Err() != nil {
		m.cleanupAsyncChatWorkspace(ctx, id, ws, workspaceProject)
		if err := ctx.Err(); err != nil {
			m.failAsyncChatSpawn(ctx, id, err)
		}
		return
	}
	m.logAsyncChatSpawnStage(id, "workspace_publish", stageStarted)
	stageStarted = time.Now()
	if err := m.restoreAttachments(ctx, id, ws); err != nil {
		m.failAsyncChatSpawn(ctx, id, wrapSpawnStage(id, ErrSpawnAttachments, err))
		return
	}
	m.logAsyncChatSpawnStage(id, "attachment_restore", stageStarted)

	record, err := m.getRecord(ctx, id)
	if err != nil {
		m.cleanupAsyncChatWorkspace(ctx, id, ws, workspaceProject)
		m.failAsyncChatSpawn(ctx, id, err)
		return
	}
	if err := ctx.Err(); err != nil {
		m.cleanupAsyncChatWorkspace(ctx, id, ws, workspaceProject)
		m.failAsyncChatSpawn(ctx, id, err)
		return
	}
	stageStarted = time.Now()
	started := BeginSpawnStep(ctx, SpawnStepAgentStart)
	if _, err := m.launchChatController(ctx, chatSpawn{
		cfg:              in.cfg,
		project:          in.project,
		projectKind:      in.projectKind,
		record:           record,
		workspace:        ws,
		workspaceProject: workspaceProject,
		prompt:           in.prompt,
		systemPrompt:     in.systemPrompt,
		systemPromptFile: in.systemPromptFile,
		promptQueued:     true,
	}); err != nil {
		started(err)
		m.failAsyncChatSpawn(ctx, id, err)
		return
	}
	started(nil)
	releaseWorkspaceGate()
	m.logAsyncChatSpawnStage(id, "controller_start", stageStarted)
	stageStarted = time.Now()
	if err := m.chat.DrainChatQueue(ctx, id); err != nil {
		m.failAsyncChatSpawn(ctx, id, wrapSpawnStage(id, ErrSpawnDeliverPrompt, err))
		return
	}
	m.logAsyncChatSpawnStage(id, "queue_drain", stageStarted)
	taskName, cancelNaming = m.startTaskNaming(in.cfg, in.record, in.project.Config)
	stageStarted = time.Now()
	if _, err := m.setProvisionState(ctx, id, domain.SessionProvisionReady, ""); err != nil {
		m.failAsyncChatSpawn(ctx, id, err)
		return
	}
	m.logAsyncChatSpawnStage(id, "mark_ready", stageStarted)
	progress.complete(ctx, domain.SessionProvisionStepAgent)
	namingHandedOff = true
	m.nameTaskBranchLater(ctx, record, ws, workspaceProject, taskName, cancelNaming)
	m.logAsyncChatSpawnStage(id, "total", totalStarted)
}

type provisionProgress struct {
	m     *Manager
	id    domain.SessionID
	steps []domain.SessionProvisionStep
}

func (m *Manager) startProvisionProgress(ctx context.Context, id domain.SessionID, plan []domain.SessionProvisionStepID) *provisionProgress {
	p := &provisionProgress{m: m, id: id, steps: make([]domain.SessionProvisionStep, len(plan))}
	for i, step := range plan {
		p.steps[i] = domain.SessionProvisionStep{ID: step, Status: domain.SessionProvisionStepPending}
	}
	p.startNext()
	p.publish(ctx)
	return p
}

func (p *provisionProgress) complete(ctx context.Context, id domain.SessionProvisionStepID) {
	i := slices.IndexFunc(p.steps, func(step domain.SessionProvisionStep) bool {
		return step.ID == id && step.Status == domain.SessionProvisionStepRunning
	})
	if i < 0 {
		return
	}
	now := p.m.clock()
	p.steps[i].Status, p.steps[i].EndedAt = domain.SessionProvisionStepDone, &now
	p.startNext()
	p.publish(ctx)
}

func (p *provisionProgress) startNext() {
	i := slices.IndexFunc(p.steps, func(step domain.SessionProvisionStep) bool {
		return step.Status == domain.SessionProvisionStepPending
	})
	if i < 0 {
		return
	}
	now := p.m.clock()
	p.steps[i].Status, p.steps[i].StartedAt = domain.SessionProvisionStepRunning, &now
}

func (p *provisionProgress) publish(ctx context.Context) {
	if err := p.m.store.SetSessionProvisionSteps(ctx, p.id, p.steps, p.m.clock()); err != nil {
		p.m.logger.Warn("spawn: publish start-up progress", "sessionID", p.id, "error", err)
	}
}

func provisionPlan(in asyncChatSpawn, reusePublishedWorkspace bool) []domain.SessionProvisionStepID {
	var plan []domain.SessionProvisionStepID
	if !reusePublishedWorkspace && (!in.retry || len(in.record.ProvisionSteps) == 0) {
		if in.preparation == nil {
			plan = append(plan, domain.SessionProvisionStepFetch)
		}
		plan = append(plan, domain.SessionProvisionStepWorktree)
		if slices.ContainsFunc(in.project.Config.PostCreate, func(command string) bool {
			return strings.TrimSpace(command) != ""
		}) {
			plan = append(plan, domain.SessionProvisionStepSetup)
		}
	}
	if in.retry && len(in.record.ProvisionSteps) > 0 {
		for _, step := range in.record.ProvisionSteps {
			if step.ID != domain.SessionProvisionStepAgent && step.Status != domain.SessionProvisionStepDone {
				plan = append(plan, step.ID)
			}
		}
	}
	return append(plan, domain.SessionProvisionStepAgent)
}

func (m *Manager) logAsyncChatSpawnStage(id domain.SessionID, stage string, started time.Time) {
	m.logger.Info("spawn: asynchronous chat stage",
		"sessionID", id,
		"stage", stage,
		"duration", time.Since(started),
	)
}

func (m *Manager) failAsyncChatSpawn(ctx context.Context, id domain.SessionID, cause error) {
	cause = classifySpawnError(ctx, RelabelSpawnTimeout(cause, ctx))
	m.logger.Error("spawn: asynchronous chat start failed", "sessionID", id, "error", cause)
	cleanupCtx, cancel := spawnRollbackContext(ctx)
	defer cancel()
	captureBackgroundFault(cleanupCtx, cause, "chat_spawn", "")
	m.stopChatBestEffort(cleanupCtx, id)
	message := cause.Error()
	var setupErr *SetupStepError
	if errors.As(cause, &setupErr) {
		message = setupErr.Error()
	}
	if _, err := m.setProvisionStateCause(cleanupCtx, id, domain.SessionProvisionFailed, message, cause); err != nil {
		m.logger.Error("spawn: record failed start", "sessionID", id, "error", err)
		captureBackgroundFault(cleanupCtx, err, "chat_spawn", "record_failure")
	}
}

func (m *Manager) retryFailedChatSpawn(ctx context.Context, rec domain.SessionRecord, releaseHarness func()) (RestoreResult, bool, error) {
	if (rec.Kind != domain.KindWorker && rec.Kind != domain.KindOrchestrator) || domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat || m.chat == nil {
		return RestoreResult{}, false, fmt.Errorf("retry start %s: %w", rec.ID, ports.ErrChatUnsupported)
	}
	if m.chat.HasLiveChatController(rec.ID) {
		if _, err := m.setProvisionState(ctx, rec.ID, domain.SessionProvisionProvisioning, ""); err != nil {
			return RestoreResult{}, false, err
		}
		progress := m.startProvisionProgress(ctx, rec.ID, []domain.SessionProvisionStepID{domain.SessionProvisionStepAgent})
		if err := m.chat.DrainChatQueue(ctx, rec.ID); err != nil {
			cleanupCtx, cancel := spawnRollbackContext(ctx)
			defer cancel()
			if _, writeErr := m.setProvisionState(cleanupCtx, rec.ID, domain.SessionProvisionFailed, err.Error()); writeErr != nil {
				return RestoreResult{}, false, errors.Join(err, writeErr)
			}
			return RestoreResult{}, false, err
		}
		if _, err := m.setProvisionState(ctx, rec.ID, domain.SessionProvisionReady, ""); err != nil {
			cleanupCtx, cancel := spawnRollbackContext(ctx)
			defer cancel()
			if _, writeErr := m.setProvisionState(cleanupCtx, rec.ID, domain.SessionProvisionFailed, err.Error()); writeErr != nil {
				return RestoreResult{}, false, errors.Join(err, writeErr)
			}
			return RestoreResult{}, false, err
		}
		progress.complete(ctx, domain.SessionProvisionStepAgent)
		ready, err := m.getRecord(ctx, rec.ID)
		return RestoreResult{Session: ready, Mode: RestoreModeNative}, false, err
	}
	if rec.Metadata.ProviderConversationID != "" {
		var err error
		rec, err = m.setProvisionState(ctx, rec.ID, domain.SessionProvisionProvisioning, "")
		if err != nil {
			return RestoreResult{}, false, err
		}
		progress := m.startProvisionProgress(ctx, rec.ID, []domain.SessionProvisionStepID{domain.SessionProvisionStepAgent})
		fail := func(cause error) (RestoreResult, bool, error) {
			cleanupCtx, cancel := spawnRollbackContext(ctx)
			defer cancel()
			if _, err := m.setProvisionState(cleanupCtx, rec.ID, domain.SessionProvisionFailed, cause.Error()); err != nil {
				return RestoreResult{}, false, errors.Join(cause, err)
			}
			return RestoreResult{}, false, cause
		}
		resumed, err := m.resumeAgentRecordWithPolicy(ctx, "retry start", rec, false)
		if err != nil {
			return fail(err)
		}
		if err := m.chat.DrainChatQueue(ctx, rec.ID); err != nil {
			return fail(err)
		}
		resumed.Session, err = m.setProvisionState(ctx, rec.ID, domain.SessionProvisionReady, "")
		if err != nil {
			return fail(err)
		}
		progress.complete(ctx, domain.SessionProvisionStepAgent)
		resumed.Session, err = m.getRecord(ctx, rec.ID)
		if err != nil {
			return RestoreResult{}, false, err
		}
		return resumed, false, nil
	}
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return RestoreResult{}, false, err
	}
	systemPrompt, err := m.buildSystemPrompt(ctx, rec.Kind, rec.ProjectID, rec.ID, rec.RequestedOutput, true)
	if err != nil {
		return RestoreResult{}, false, err
	}
	systemPromptFile, err := m.prepareSystemPromptFile(rec.ID, rec.Harness, systemPrompt)
	if err != nil {
		return RestoreResult{}, false, wrapSpawnStage(rec.ID, ErrSpawnSystemPrompt, err)
	}
	config := restoredAgentConfig(rec, project.Config)
	cfg := ports.SpawnConfig{
		ProjectID: rec.ProjectID, IssueID: rec.IssueID, Kind: rec.Kind,
		Harness: rec.Harness, RequestedMode: domain.SessionModeChat,
		AgentConfig: config, AgentConfigResolved: true, Async: true,
	}
	if m.forkRetry != nil && m.isSessionFork(ctx, rec.ID) {
		fork, err := m.forkRetry(ctx, rec)
		if err != nil {
			return RestoreResult{}, false, fmt.Errorf("retry fork %s: %w", rec.ID, err)
		}
		cfg.Fork = fork
	}
	if rec.Harness == domain.HarnessCodex {
		cfg.EffortOverride = true
		resolved, err := m.resolveAgentConfig(ctx, cfg, domain.ProjectConfig{})
		if errors.Is(err, ports.ErrUnsupportedEffort) {
			cfg.AgentConfig.Effort = ""
			resolved, err = m.resolveAgentConfig(ctx, cfg, domain.ProjectConfig{})
		}
		if err != nil {
			return RestoreResult{}, false, err
		}
		cfg.AgentConfig = resolved
	}
	projectKind := projectKindForSession(project, rec.ProjectID)
	branch := rec.Metadata.Branch
	if branch == "" {
		branch = DefaultSpawnBranch(rec.ID, rec.Kind, sessionPrefix(project), projectKind, m.dataDir)
	}
	retried, _, _, err := m.beginAsyncChatSpawn(ctx, asyncChatSpawn{
		cfg: cfg, project: project, projectKind: projectKind,
		record: rec, branch: branch, systemPrompt: systemPrompt, systemPromptFile: systemPromptFile,
		retry: true, releaseHarness: releaseHarness,
	})
	if err != nil {
		return RestoreResult{}, false, err
	}
	return RestoreResult{Session: retried, Mode: RestoreModeSavedPrompt}, true, nil
}

func (m *Manager) cleanupAsyncChatWorkspace(ctx context.Context, id domain.SessionID, ws ports.WorkspaceInfo, workspaceProject *ports.WorkspaceProjectInfo) {
	cleanupCtx, cancel := spawnRollbackContext(ctx)
	defer cancel()
	destroyed := m.destroySpawnWorkspace(cleanupCtx, ws, workspaceProject)
	cancel()
	cleanupCtx, cancel = spawnRollbackContext(ctx)
	defer cancel()
	if destroyed {
		m.clearProvisionedWorkspace(cleanupCtx, id, ws.Path)
	} else {
		updated, err := m.store.SetSessionProvisionedWorkspace(
			cleanupCtx, id, ws.Branch, ws.Path, ws.RepoPath, m.clock())
		if err != nil || !updated {
			m.logger.Warn("spawn: preserve failed workspace", "sessionID", id, "workspacePath", ws.Path, "updated", updated, "error", err)
		}
	}
}

func (m *Manager) setProvisionState(
	ctx context.Context,
	id domain.SessionID,
	state domain.SessionProvisionState,
	message string,
) (domain.SessionRecord, error) {
	return m.setProvisionStateCause(ctx, id, state, message, nil)
}

type ProvisionObserver func(ctx context.Context, rec domain.SessionRecord, state domain.SessionProvisionState, cause error)

func (m *Manager) SetProvisionObserver(fn ProvisionObserver) { m.provisionObserver = fn }

func (m *Manager) setProvisionStateCause(
	ctx context.Context,
	id domain.SessionID,
	state domain.SessionProvisionState,
	message string,
	cause error,
) (domain.SessionRecord, error) {
	if _, err := m.store.SetSessionProvisionState(ctx, id, state, message, m.clock()); err != nil {
		return domain.SessionRecord{}, err
	}
	rec, err := m.getRecord(ctx, id)
	if err == nil && m.provisionObserver != nil {
		if cause == nil && state == domain.SessionProvisionFailed {
			cause = errors.New("asynchronous start failed")
		}
		m.provisionObserver(ctx, rec, state, cause)
	}
	return rec, err
}

func (m *Manager) FailInterruptedProvisioning(ctx context.Context) error {
	recs, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return fmt.Errorf("list sessions for interrupted starts: %w", err)
	}
	_, err = m.failInterruptedProvisioningRecords(ctx, recs)
	return err
}

func (m *Manager) failInterruptedProvisioningRecords(ctx context.Context, recs []domain.SessionRecord) ([]domain.SessionRecord, error) {
	var failures []error
	var retries []domain.SessionRecord
	for _, rec := range recs {
		if rec.IsTerminated || rec.IsTaskPreparation || !rec.ProvisionState.IsProvisioning() {
			continue
		}
		if _, err := m.setProvisionState(ctx, rec.ID, domain.SessionProvisionFailed,
			"Orchestrator restarted before this session finished starting"); err != nil {
			failures = append(failures, fmt.Errorf("session %s: %w", rec.ID, err))
			retries = append(retries, rec)
		}
	}
	return retries, errors.Join(failures...)
}

func (m *Manager) runInBackground(work func()) {
	m.backgroundWorkers.Add(1)
	tracked := func() {
		defer m.backgroundWorkers.Done()
		work()
	}
	if m.runBackground != nil {
		m.runBackground(tracked)
		return
	}
	go tracked()
}

func (m *Manager) WaitBackgroundWorkers(ctx context.Context) error {
	return waitForWorkers(ctx, &m.backgroundWorkers)
}

func waitForWorkers(ctx context.Context, workers *sync.WaitGroup) error {
	done := make(chan struct{})
	go func() {
		workers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) cancelAsyncChatSpawn(ctx context.Context, id domain.SessionID) error {
	m.asyncChatSpawnsMu.Lock()
	run := m.asyncChatSpawns[id]
	m.asyncChatSpawnsMu.Unlock()
	if run != nil {
		run.cancel()
		select {
		case <-run.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok || !rec.ProvisionState.IsProvisioning() {
		return err
	}
	_, err = m.setProvisionStateCause(ctx, id, domain.SessionProvisionFailed, "Session start was cancelled", context.Canceled)
	return err
}

func (m *Manager) clearProvisionedWorkspace(ctx context.Context, id domain.SessionID, workspacePath string) {
	cleanupCtx, cancel := spawnRollbackContext(ctx)
	defer cancel()
	rec, ok, err := m.store.GetSession(cleanupCtx, id)
	if err != nil || !ok || rec.Metadata.WorkspacePath != workspacePath {
		return
	}
	rec.Metadata.Branch = ""
	rec.Metadata.WorkspacePath = ""
	rec.Metadata.WorkspaceRepoPath = ""
	if err := m.store.UpdateSession(cleanupCtx, rec); err != nil {
		m.logger.Warn("spawn: clear removed workspace", "sessionID", id, "error", err)
	}
}
