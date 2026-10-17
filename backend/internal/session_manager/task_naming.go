package sessionmanager

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/pkg/tasknaming"
)

// startTaskNaming overlaps the one cosmetic model call with workspace preparation.
// The waiter also observes the deadline if a provider is slow to cancel.
func (m *Manager) startTaskNaming(ctx context.Context, cfg ports.SpawnConfig, rec domain.SessionRecord, projectConfig domain.ProjectConfig) (func() tasknaming.Name, context.CancelFunc) {
	if !cfg.NameFromPrompt || cfg.Branch != "" || cfg.Kind != domain.KindWorker || strings.TrimSpace(cfg.Prompt) == "" {
		return nil, func() {}
	}
	fallback := tasknaming.Fallback(cfg.Prompt, string(rec.ID))
	runner, ok := m.chat.(chatBackgroundTaskRunner)
	if !ok {
		return func() tasknaming.Name { return fallback }, func() {}
	}
	namingCtx, cancel := context.WithTimeout(ctx, tasknaming.Timeout)
	done := make(chan tasknaming.Name, 1)
	slots := m.taskNamingSlots
	select {
	case slots <- struct{}{}:
		config := cfg.AgentConfig
		if !cfg.AgentConfigResolved {
			config = applySpawnAgentConfig(effectiveAgentConfig(cfg.Harness, cfg.Kind, projectConfig), config)
		}
		// Chat has already resolved model/effort; TUI callers carry their resolved
		// config below. This temporary call never owns the worker's conversation.
		rec.Metadata.Model, rec.Metadata.Effort = config.Model, config.Effort
		m.runInBackground(func() {
			defer func() { <-slots }()
			started := time.Now()
			raw, err := m.runRecordBackgroundTask(namingCtx, rec, tasknaming.SystemPrompt, cfg.Prompt, runner.RunBackgroundTask)
			name := fallback
			if err == nil && namingCtx.Err() == nil {
				name = tasknaming.Parse(raw, fallback)
			}
			m.logger.Info("task naming", "sessionID", rec.ID, "durationMs", time.Since(started).Milliseconds(), "generated", name.Title != "", "error", err)
			if cfg.KeepDisplayName {
				name.Title = ""
			}
			done <- name
		})
	default:
		// ponytail: overflow uses the local slug; queue only if skipped AI names become a problem.
		done <- fallback
	}
	return func() tasknaming.Name {
		return tasknaming.Wait(namingCtx, done, fallback)
	}, cancel
}

func (m *Manager) nameTaskWorkspace(ctx context.Context, rec domain.SessionRecord, ws ports.WorkspaceInfo, project *ports.WorkspaceProjectInfo, name tasknaming.Name) (ports.WorkspaceInfo, *ports.WorkspaceProjectInfo, error) {
	namer, ok := m.workspace.(ports.WorkspaceTaskNamer)
	if !ok || name.Slug == "" || ws.Branch == "" {
		return ws, project, nil
	}
	if name.Title != "" {
		if _, err := m.store.RenameSessionIfDisplayName(ctx, rec.ID, rec.DisplayName, name.Title, m.clock()); err != nil {
			m.logger.Warn("apply generated task title", "sessionID", rec.ID, "error", err)
		}
	}
	info := ports.WorkspaceProjectInfo{Root: ws}
	if project != nil {
		info = *project
	}
	info, namingErr := namer.NameTaskWorkspace(ctx, info, aoBranch(generatedBranchNamespace(m.dataDir), name.Slug, "root"), name.Slug)
	persistErr := m.persistTaskWorkspace(ctx, rec.ID, info, rec.Metadata.WorkspacePath)
	if namingErr == nil && persistErr == nil {
		persistErr = m.completeNamedWorktree(ctx, info)
	}
	if project != nil {
		project = &info
	}
	return info.Root, project, errors.Join(namingErr, persistErr)
}

// Complete synchronous naming progress before acknowledging its move journal.
// Recovery must not leave a repaired final branch hidden behind a stale shimmer.
func (m *Manager) completeNamedWorktree(ctx context.Context, info ports.WorkspaceProjectInfo) error {
	rec, found, err := m.store.GetSession(ctx, info.Root.SessionID)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("naming session no longer exists")
	}
	if rec.ProvisionState.WithDefault() == domain.SessionProvisionReady {
		for i, step := range rec.ProvisionSteps {
			if step.ID != domain.SessionProvisionStepWorktree || step.Status == domain.SessionProvisionStepDone {
				continue
			}
			steps := slices.Clone(rec.ProvisionSteps)
			now := m.clock()
			steps[i].Status, steps[i].EndedAt = domain.SessionProvisionStepDone, &now
			if err := m.store.SetSessionProvisionSteps(ctx, rec.ID, steps, now); err != nil {
				return err
			}
			break
		}
	}
	return m.completeTaskNaming(ctx, info)
}

func (m *Manager) completeTaskNaming(ctx context.Context, info ports.WorkspaceProjectInfo) error {
	if recovery, ok := m.workspace.(ports.WorkspaceTaskNamingRecovery); ok {
		return recovery.CompleteTaskWorkspaceNaming(ctx, info)
	}
	return nil
}

func (m *Manager) recoverTaskNaming(ctx context.Context, rec domain.SessionRecord) (ports.WorkspaceProjectInfo, bool, error) {
	recovery, ok := m.workspace.(ports.WorkspaceTaskNamingRecovery)
	if !ok || rec.ProjectID == "" || rec.IsTerminated || rec.IsTaskPreparation || rec.Metadata.RuntimeHandleID != "" || rec.Metadata.ProviderConversationID != "" {
		return ports.WorkspaceProjectInfo{}, false, nil
	}
	info, found, err := recovery.RecoverTaskWorkspace(ctx, rec.ProjectID, rec.ID)
	if !found || err != nil {
		return info, found, err
	}
	if err := m.persistTaskWorkspace(ctx, rec.ID, info, rec.Metadata.WorkspacePath); err != nil {
		return info, true, err
	}
	return info, true, m.completeNamedWorktree(ctx, info)
}

// Persist the actual result even after a partial Git failure. The provision
// checklist separately records whether registration repair and setup finished.
func (m *Manager) persistTaskWorkspace(ctx context.Context, id domain.SessionID, info ports.WorkspaceProjectInfo, expectedPath string) error {
	persistCtx, cancel := spawnRollbackContext(ctx)
	defer cancel()
	updated, persistErr := m.store.SetSessionNamedWorkspace(persistCtx, id, expectedPath, info.Root.Branch, info.Root.Path, info.Root.RepoPath, m.clock())
	if persistErr == nil && !updated {
		persistErr = errors.New("task naming session no longer accepts this workspace")
	}
	if persistErr != nil {
		return persistErr
	}
	for _, wt := range info.Worktrees {
		persistErr = errors.Join(persistErr, m.store.UpsertSessionWorktree(persistCtx, domain.SessionWorktreeRecord{
			SessionID: id, RepoName: wt.RepoName, Branch: wt.Branch, BaseSHA: wt.BaseSHA,
			BaseRef: wt.BaseRef, CreationSHA: wt.CreationSHA, WorktreePath: wt.Path, State: "active",
		}))
	}
	return persistErr
}
