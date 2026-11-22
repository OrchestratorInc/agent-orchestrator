package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/dirseed"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/forkseed"
	"github.com/aoagents/agent-orchestrator/backend/internal/artifactversions"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/filesnapshot"
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

// dirForkSeeds copies a plain folder for a fork: a standalone agent's, or a
// Scratch project's session's. Seeds are kept under the data folder, on the
// same volume as the workspaces, so applying one is a rename.
type dirForkSeeds struct {
	// root holds the seeds; managedRoot holds the plain folders a fork may
	// copy.
	root, managedRoot string
}

func newDirForkSeeds(dataDir string) dirForkSeeds {
	return dirForkSeeds{root: filepath.Join(dataDir, dirseed.DirName), managedRoot: filepath.Join(dataDir, "worktrees")}
}

// plainSessionFolder reports whether workspace, its links resolved, is one
// session's plain folder under Orchestrator's managed root, as the scratch
// workspace adapter lays them out: standalone/sessions/<id>, or
// <project>/workers/<id> and <project>/orchestrators/<id> for a Scratch
// project. It returns the session's id. Git worktrees, the folders holding
// several sessions, and the user's own folders never match.
func plainSessionFolder(managedRoot, workspace string) (string, bool) {
	if strings.TrimSpace(workspace) == "" {
		return "", false
	}
	root, err := filepath.EvalSymlinks(managedRoot)
	if err != nil {
		return "", false
	}
	path, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", false
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		return "", false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 3 || parts[0] == ".." || parts[0] == "." {
		return "", false
	}
	switch {
	case parts[0] == "standalone" && parts[1] == "sessions":
	case parts[1] == "workers" || parts[1] == "orchestrators":
	default:
		return "", false
	}
	return parts[2], true
}

// Manages reports whether workspace is one session's plain folder; the
// user's own folders are never copied.
func (d dirForkSeeds) Manages(workspace string) bool {
	_, ok := plainSessionFolder(d.managedRoot, workspace)
	return ok
}

func (d dirForkSeeds) Capture(ctx context.Context, workspace string) (string, error) {
	seed, err := d.newSeedPath()
	if err != nil {
		return "", err
	}
	if err := dirseed.Capture(ctx, workspace, seed); err != nil {
		return "", err
	}
	return seed, nil
}

// CaptureTurn writes the files a turn left, from the folder's snapshot, to a
// new seed.
// A folder the agent made a repository, or one holding a repository it cloned,
// is refused: snapshots never record a repository's own files and history.
func (d dirForkSeeds) CaptureTurn(ctx context.Context, workspace, snapshotRef string) (string, error) {
	if _, err := os.Lstat(filepath.Join(workspace, ".git")); !errors.Is(err, os.ErrNotExist) {
		return "", sessionfork.ErrSnapshotHasRepository
	}
	seed, err := d.newSeedPath()
	if err != nil {
		return "", err
	}
	if err := filesnapshot.Export(ctx, workspace, snapshotRef, seed); err != nil {
		switch {
		case errors.Is(err, filesnapshot.ErrNestedRepository):
			return "", sessionfork.ErrSnapshotHasRepository
		case errors.Is(err, filesnapshot.ErrNotFound) || errors.Is(err, filesnapshot.ErrNotRepository):
			return "", sessionfork.ErrSnapshotMissing
		}
		return "", err
	}
	return seed, nil
}

func (d dirForkSeeds) newSeedPath() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return filepath.Join(d.root, hex.EncodeToString(b)), nil
}

// Delete removes a seed; it refuses any path outside the seeds folder.
func (d dirForkSeeds) Delete(_ context.Context, seed string) error {
	if rel, err := filepath.Rel(d.root, seed); err != nil || rel == "." || strings.Contains(rel, string(filepath.Separator)) || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("session fork: %q is not a fork seed", seed)
	}
	return dirseed.Delete(seed)
}

// folderSnapshotsDir, under the data folder, holds each plain folder's
// snapshot store, one per session.
const folderSnapshotsDir = "folder-snapshots"

// folderSnapshotStores keeps a plain session folder's turn snapshots in a git
// store of its own under the data folder, so Chat rollback can restore its
// files and a fork can start from an older turn, with nothing written into
// the folder. A folder the agent made a repository of its own keeps its
// snapshots there, as any repository does.
func folderSnapshotStores(dataDir string) filesnapshot.DetachedStores {
	managed, root := filepath.Join(dataDir, "worktrees"), filepath.Join(dataDir, folderSnapshotsDir)
	return func(workspace string) (string, bool) {
		id, ok := plainSessionFolder(managed, workspace)
		if !ok {
			return "", false
		}
		if _, err := os.Lstat(filepath.Join(workspace, ".git")); !errors.Is(err, os.ErrNotExist) {
			return "", false
		}
		return filepath.Join(root, id+".git"), true
	}
}

// folderSnapshotSessions reads whether a store's session still exists.
type folderSnapshotSessions interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// reapFolderSnapshots removes the snapshot stores of deleted sessions, and
// stores a crash left half made.
func reapFolderSnapshots(ctx context.Context, dataDir string, sessions folderSnapshotSessions, log *slog.Logger) {
	root := filepath.Join(dataDir, folderSnapshotsDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Debug("session fork: list folder snapshot stores", "err", err)
		}
		return
	}
	for _, entry := range entries {
		name, path := entry.Name(), filepath.Join(root, entry.Name())
		if strings.HasPrefix(name, ".new-store-") {
			if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) > forkSeedMaxAge {
				_ = os.RemoveAll(path)
			}
			continue
		}
		id, ok := strings.CutSuffix(name, ".git")
		if !ok {
			continue
		}
		if _, found, err := sessions.GetSession(ctx, domain.SessionID(id)); err != nil || found {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			log.Debug("session fork: remove folder snapshot store", "session", id, "err", err)
		} else {
			log.Info("session fork: removed a deleted session's folder snapshots", "session", id)
		}
	}
}

// forkSpawner spawns through the session service, so a repeated client
// request id returns the session the first click made.
type forkSpawner struct{ svc *sessionsvc.Service }

func (f forkSpawner) Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.SessionRecord, int, int, error) {
	sess, promptBytes, systemBytes, err := f.svc.Spawn(ctx, cfg)
	return sess.SessionRecord, promptBytes, systemBytes, err
}

// wireSessionFork builds the session fork service and its hooks.
func wireSessionFork(dataDir string, store *sqlite.Store, sessionSvc *sessionsvc.Service, mgr sessionLifecycle, chat *chatsvc.Service, drivers ports.ChatDriverRegistry, accounts sessionfork.Accounts, artifacts *artifactversions.Capturer, messenger ports.AgentMessenger, log *slog.Logger) *sessionfork.Service {
	const enabled = true
	// Plain folders' snapshots serve Chat rollback as well as forks.
	filesnapshot.SetDetachedStores(folderSnapshotStores(dataDir))
	terminal, _ := mgr.(terminalForkSources)
	svc := sessionfork.New(sessionfork.Deps{
		Enabled: enabled, Sessions: store, Projects: store, Lineage: store,
		Spawner: forkSpawner{svc: sessionSvc}, Sources: forkSources{chat: chat, terminal: terminal, notes: chat}, Seeds: forkSeeds{},
		DirSeeds: newDirForkSeeds(dataDir), Accounts: accounts, PRs: store, Requests: store,
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

// reapDirForkSeeds removes plain-folder copies a crash left behind.
func reapDirForkSeeds(dataDir string, log *slog.Logger) {
	if removed, err := dirseed.Reap(filepath.Join(dataDir, dirseed.DirName), forkSeedMaxAge, time.Now()); err != nil {
		log.Debug("session fork: folder seed cleanup", "err", err)
	} else if removed > 0 {
		log.Info("session fork: removed leftover folder copies", "count", removed)
	}
}

// reapForkSeeds removes fork file snapshots a crash left behind.
func reapForkSeeds(ctx context.Context, dataDir string, store *sqlite.Store, log *slog.Logger) {
	reapDirForkSeeds(dataDir, log)
	reapFolderSnapshots(ctx, dataDir, store, log)
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
