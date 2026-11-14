package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/forkseed"
	"github.com/aoagents/agent-orchestrator/backend/internal/artifactversions"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionfork"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// forkSeedMaxAge is how long a fork's file snapshot may outlive its spawn
// before the cleanup pass treats it as a crash leftover.
const forkSeedMaxAge = time.Hour

// prMovedNoticeBudget bounds telling a session that one of its PRs moved.
const prMovedNoticeBudget = 2 * time.Minute

// turnArtifactSnapshotBudget bounds keeping a session's artifacts as a turn
// starts; a turn never waits longer for it.
const turnArtifactSnapshotBudget = 3 * time.Second

type terminalForkSources interface {
	TerminalForkSource(ctx context.Context, rec domain.SessionRecord, turnID string) (sessionfork.Source, error)
	// ForkTerminalOutput is the end of a terminal session's screen, or "".
	ForkTerminalOutput(ctx context.Context, rec domain.SessionRecord) string
}

// forkHandoffNotes builds the text copy of a conversation a text-handoff
// fork starts with.
type forkHandoffNotes interface {
	ForkHandoffNote(ctx context.Context, source domain.SessionRecord, target ports.ForkTarget, terminalOutput string, upTo time.Time) (string, error)
}

// forkSources routes a fork to the Chat service or the terminal manager.
type forkSources struct {
	chat     *chatsvc.Service
	terminal terminalForkSources
	notes    forkHandoffNotes
}

func (f forkSources) ForkSource(ctx context.Context, rec domain.SessionRecord, turnID string) (sessionfork.Source, error) {
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		return f.chat.SessionForkSource(ctx, rec, turnID)
	}
	if f.terminal == nil {
		return sessionfork.Source{Refusal: sessionfork.CodeAgentUnsupported}, nil
	}
	source, err := f.terminal.TerminalForkSource(ctx, rec, turnID)
	if err != nil || f.notes == nil {
		return source, err
	}
	switch source.Refusal {
	case sessionfork.CodeAgentUnsupported, sessionfork.CodeNativeMissing:
		// The agent can't copy its own conversation: a fresh agent gets AO's
		// record of it and the end of the terminal screen, as text. Both are
		// taken when the fork captures the files, so they match them even if
		// the original moves on while the fork is set up.
		original := rec
		var (
			frozen bool
			screen string
			upTo   time.Time
		)
		return sessionfork.Source{
			ContextMode: domain.ForkContextTextHandoff,
			Freeze: func(ctx context.Context) {
				frozen, screen, upTo = true, f.terminal.ForkTerminalOutput(ctx, original), time.Now()
			},
			Handoff: func(ctx context.Context, target ports.ForkTarget) (string, error) {
				if !frozen {
					// A retried fork that was planned without capturing.
					screen, upTo = f.terminal.ForkTerminalOutput(ctx, original), time.Now()
				}
				return f.notes.ForkHandoffNote(ctx, original, target, screen, upTo)
			},
		}, nil
	}
	return source, nil
}

type forkSeeds struct{}

func (forkSeeds) Capture(ctx context.Context, workspace, ref string) (string, error) {
	head, err := forkseed.Capture(ctx, workspace, ref)
	if errors.Is(err, forkseed.ErrNotRepository) {
		return "", sessionfork.ErrNotRepository
	}
	return head, err
}

func (forkSeeds) Delete(ctx context.Context, workspace, ref string) error {
	return forkseed.Delete(ctx, workspace, ref)
}

func (forkSeeds) SnapshotHead(ctx context.Context, workspace, ref string) (string, error) {
	head, err := forkseed.SnapshotHead(ctx, workspace, ref)
	if errors.Is(err, forkseed.ErrSnapshotMissing) {
		return "", sessionfork.ErrSnapshotMissing
	}
	return head, err
}

// forkSpawner spawns through the session service, so a repeated client
// request id returns the session the first click made.
type forkSpawner struct{ svc *sessionsvc.Service }

func (f forkSpawner) Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.SessionRecord, int, int, error) {
	sess, promptBytes, systemBytes, err := f.svc.Spawn(ctx, cfg)
	return sess.SessionRecord, promptBytes, systemBytes, err
}

// wireSessionFork builds the session fork service and its hooks.
func wireSessionFork(store *sqlite.Store, sessionSvc *sessionsvc.Service, mgr sessionLifecycle, chat *chatsvc.Service, drivers ports.ChatDriverRegistry, accounts sessionfork.Accounts, artifacts *artifactversions.Capturer, messenger ports.AgentMessenger, log *slog.Logger) *sessionfork.Service {
	const enabled = true
	terminal, _ := mgr.(terminalForkSources)
	svc := sessionfork.New(sessionfork.Deps{
		Enabled: enabled, Sessions: store, Projects: store, Lineage: store,
		Spawner: forkSpawner{svc: sessionSvc}, Sources: forkSources{chat: chat, terminal: terminal, notes: chat}, Seeds: forkSeeds{},
		Accounts: accounts, PRs: store, Requests: store,
	})
	if !enabled {
		return svc
	}
	chatsvc.EnableForkSnapshots(true)
	if artifacts != nil {
		chatsvc.SetTurnArtifactSnapshot(func(ctx context.Context, id domain.SessionID, turnIDs []string) {
			// Bounded: it runs as the turn starts.
			ctx, cancel := context.WithTimeout(ctx, turnArtifactSnapshotBudget)
			defer cancel()
			rec, found, err := store.GetSession(ctx, id)
			if err != nil || !found {
				return
			}
			if err := artifacts.SnapshotTurn(ctx, rec, turnIDs); err != nil {
				log.Debug("session fork: keep artifacts at turn start", "session", id, "err", err)
			}
		})
	}
	if wired, ok := mgr.(*sessionmanager.Manager); ok {
		forker, cleaner := claudeWorkspaceForker(drivers)
		wired.SetClaudeSessionForker(forker, cleaner)
		wired.SetForkRetrier(svc.RetrySpawnFork)
	}
	sessionSvc.SetPRMovedNotifier(func(ctx context.Context, from, to domain.SessionID, pr domain.PullRequest) {
		// Told in the background: waking an agent can take a while, and the
		// claim that moved the PR must not wait for it.
		go func() {
			// Bounded, so an agent that never answers can't hold its chat's
			// send lock; long enough to start an agent that isn't running.
			ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), prMovedNoticeBudget)
			defer cancel()
			title := string(to)
			if rec, found, err := store.GetSession(ctx, to); err == nil && found {
				title = sessionfork.Title(rec)
			}
			message := fmt.Sprintf("PR #%d moved to %s (%s). It now belongs to that session: don't push to it or act on its CI or reviews from here.", pr.Number, title, to)
			queue := func(ctx context.Context, id domain.SessionID, text string) error {
				_, err := chat.QueueUserMessage(ctx, id, ports.ChatUserMessage{Text: text, Origin: domain.MessageOriginAutomation})
				return err
			}
			resume := func(ctx context.Context, id domain.SessionID) error {
				_, err := sessionSvc.ResumeAgent(ctx, id)
				return err
			}
			if err := tellPRMoved(ctx, messenger.Send, queue, resume, from, message); err != nil {
				log.Warn("session fork: tell session its PR moved", "session", from, "pr", pr.URL, "err", err)
			}
		}()
	})
	return svc
}

// tellPRMoved tells a session that one of its PRs moved. A Chat session whose
// agent isn't running gets the notice queued for when it wakes; only one
// whose agent never started, which has no queue, is started to be told.
func tellPRMoved(ctx context.Context, send, queue func(context.Context, domain.SessionID, string) error, resume func(context.Context, domain.SessionID) error, id domain.SessionID, message string) error {
	err := send(ctx, id, message)
	if !errors.Is(err, chatsvc.ErrNoController) {
		return err
	}
	err = queue(ctx, id, message)
	if !errors.Is(err, chatsvc.ErrNotProvisioning) {
		return err
	}
	if err := resume(ctx, id); err != nil {
		return fmt.Errorf("start agent: %w", err)
	}
	return send(ctx, id, message)
}

type workspaceForkCleaner interface {
	DeleteWorkspaceFork(ctx context.Context, providerConversationID, workspace string, env map[string]string) error
}

func claudeWorkspaceForker(drivers ports.ChatDriverRegistry) (sessionmanager.ClaudeSessionForker, sessionmanager.ClaudeSessionCleaner) {
	driver, err := drivers.Driver(domain.HarnessClaudeCode)
	if err != nil {
		return nil, nil
	}
	forker, ok := driver.(ports.ChatWorkspaceForker)
	if !ok {
		return nil, nil
	}
	fork := func(ctx context.Context, sourceID, sourceWorkspace string, target ports.ForkTarget) (string, error) {
		return forker.ForkIntoWorkspace(ctx, sourceID, sourceWorkspace, "", ports.ChatStartConfig{
			SessionID: target.SessionID, DataDir: target.DataDir, WorkspacePath: target.WorkspacePath, Env: target.Env,
		})
	}
	var cleanup sessionmanager.ClaudeSessionCleaner
	if cleaner, ok := driver.(workspaceForkCleaner); ok {
		cleanup = cleaner.DeleteWorkspaceFork
	}
	return fork, cleanup
}

// reapForkSeeds removes fork file snapshots a crash left behind.
func reapForkSeeds(ctx context.Context, store *sqlite.Store, log *slog.Logger) {
	projects, err := store.ListProjects(ctx)
	if err != nil {
		log.Warn("session fork: list projects for seed cleanup", "err", err)
		return
	}
	for _, project := range projects {
		if project.Path == "" || project.Kind.WithDefault() == domain.ProjectKindWorkspace {
			continue
		}
		if _, err := os.Stat(project.Path); err != nil {
			continue
		}
		if removed, err := forkseed.Reap(ctx, project.Path, forkSeedMaxAge, time.Now()); err != nil {
			log.Debug("session fork: seed cleanup", "project", project.ID, "err", err)
		} else if removed > 0 {
			log.Info("session fork: removed leftover file snapshots", "project", project.ID, "count", removed)
		}
	}
}
