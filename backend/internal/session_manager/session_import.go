package sessionmanager

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionimport"
)

// ImportArchiveRefresher rereads an imported conversation before its first open.
type ImportArchiveRefresher interface {
	RefreshArchive(context.Context, domain.SessionRecord) error
}

// SetImportArchiveRefresher late-binds the import service; nil skips the refresh.
func (m *Manager) SetImportArchiveRefresher(refresher ImportArchiveRefresher) {
	m.importArchive = refresher
}

// refreshImportArchive keeps turns added in another app since the import in the
// visible history. The copy taken at import remains when the source is unreadable.
func (m *Manager) refreshImportArchive(ctx context.Context, rec domain.SessionRecord) {
	if m.importArchive == nil {
		return
	}
	if err := m.importArchive.RefreshArchive(ctx, rec); err != nil {
		m.logger.Warn("imported history refresh failed; keeping the import-time copy", "sessionID", rec.ID, "error", err)
	}
}

// importReplayedThrough is the last sequence of the provider replay projected
// while adopting an import. Native history import runs before ControllerReady,
// so no AO turn can sit at or below it. Adoption records it once, so a failed
// read fails the adoption instead of storing a boundary that hides nothing.
func (m *Manager) importReplayedThrough(ctx context.Context, id domain.SessionID) (int64, error) {
	store, ok := m.store.(chatProviderOwnershipStore)
	if !ok {
		return 0, nil
	}
	conversation, err := store.ConversationForSession(ctx, id)
	if err != nil {
		return 0, fmt.Errorf("read the imported history boundary: %w", err)
	}
	return conversation.LatestSequence, nil
}

// ErrSessionOpenElsewhere requires the other writer to close; AO never takes it over.
var ErrSessionOpenElsewhere = errors.New("this is open elsewhere; close it there to continue here")

// applyImportedConfigEnv points a launch at the provider home that owns the
// imported conversation. It leaves a launch alone when that home already holds
// the conversation: Claude Code keys its keychain login by CLAUDE_CONFIG_DIR, so
// setting even the default path logs a keychain user out, and an AO account's
// folder links the computer's history, so replacing it would run the chat
// under another login than the session's account.
//
// Kimi Code stores every session strictly under its home (KIMI_CODE_HOME, else
// ~/.kimi-code); KIMI_SHARE_DIR is only the legacy kimi-cli migration source and
// there is no sessions-dir or session-path override, so an imported Kimi chat
// has to launch in the source home to be found. This stays equivalent to a
// managed Kimi chat: AO's managed Kimi home only mirrors that same login
// (ShareCredentials symlinks the managed credentials to it and seeds its config
// from it), and standing instructions reach the ACP session through the
// workspace .kimi-code/AGENTS.md and the first-prompt preamble, both independent
// of the home. The config.toml lifecycle hooks are a TUI-only mechanism the ACP
// chat driver does not use, so pointing at the source home drops nothing a chat
// relied on.
func (m *Manager) applyImportedConfigEnv(ctx context.Context, rec domain.SessionRecord, env map[string]string) {
	source := rec.Metadata.ImportSource
	if source == nil || rec.Metadata.ProviderConversationID != source.NativeID {
		return
	}
	key := map[domain.AgentHarness]string{domain.HarnessClaudeCode: "CLAUDE_CONFIG_DIR", domain.HarnessCodex: "CODEX_HOME", domain.HarnessDroid: "FACTORY_HOME_OVERRIDE", domain.HarnessOpenCode: "OPENCODE_DATA_DIR", domain.HarnessPi: "PI_CODING_AGENT_DIR", domain.HarnessKimi: "KIMI_CODE_HOME"}[rec.Harness]
	if key == "" {
		return
	}
	if agent, ok := m.agents.Agent(rec.Harness); ok {
		if provider, ok := agent.(ports.AgentNativeSessionConfigProvider); ok {
			if current, err := provider.NativeSessionConfigDir(ctx, env); err == nil {
				if sameDirectory(current, source.ConfigDir) {
					return
				}
				if rel, err := filepath.Rel(source.ConfigDir, rec.Metadata.NativeTranscriptPath); err == nil && filepath.IsLocal(rel) {
					// Codex compresses an inactive rollout in place and restores it on resume.
					plain := strings.TrimSuffix(rel, ".zst")
					for _, name := range []string{plain, plain + ".zst"} {
						if _, err := os.Stat(filepath.Join(current, name)); err == nil {
							return
						}
					}
				}
			}
		}
	}
	value := source.ConfigDir
	if rec.Harness == domain.HarnessDroid {
		// FACTORY_HOME_OVERRIDE is a home override Droid appends ".factory" to,
		// not the state dir itself, so point it at the parent of the config dir.
		value = filepath.Dir(source.ConfigDir)
	}
	setProtectedEnv(env, key, value, envKeysCaseInsensitive)
}

// relocateImportedSession lets a provider that finds a session by the directory
// it runs in (OMP, Qwen, Kimchi) load the imported session from AO's launch
// directory, before the first resume. Providers that find a session by id from
// any directory do not implement the relocator and need nothing.
//
// It records the launch directory as ResumeCwd, and every later launch of the
// session is pinned to it (pinnedImportLaunchPath), so the provider keeps
// finding the same copy, which then holds AO's turns. If the copy is still not
// where the provider looks, the required first-open replay comes back without
// messages and the import fails rather than continuing an empty chat.
func (m *Manager) relocateImportedSession(ctx context.Context, rec domain.SessionRecord, launchPath string) (domain.SessionRecord, error) {
	source := rec.Metadata.ImportSource
	if source == nil || m.agents == nil {
		return rec, nil
	}
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return rec, nil
	}
	relocator, ok := agent.(ports.AgentImportedSessionRelocator)
	if !ok {
		return rec, nil
	}
	err := relocator.RelocateImportedSession(ctx, ports.ImportedSessionRelocation{
		NativeSessionID: source.NativeID,
		ConfigDir:       source.ConfigDir,
		TranscriptPath:  rec.Metadata.NativeTranscriptPath,
		SourceCWD:       source.CWD,
		LaunchCWD:       launchPath,
	})
	if err != nil {
		return rec, fmt.Errorf("%w: %w", importNotice("AO could not prepare this conversation for its workspace. See the AO daemon log for details."), err)
	}
	if source.ResumeCwd == launchPath {
		return rec, nil
	}
	next := *source
	next.ResumeCwd = launchPath
	updated, err := m.store.SetSessionImportWorkspace(ctx, rec.ID, source, &next, rec.Metadata, m.clock())
	if err != nil {
		return rec, err
	}
	if !updated {
		return rec, importNotice("session changed while preparing the conversation; retry")
	}
	rec.Metadata.ImportSource = &next
	return rec, nil
}

// pinnedImportLaunchPath is the launch directory of a relocated import: the one
// its provider's copy was prepared for. It must stay inside the workspace, and
// is recreated when something removed it, so the provider never looks for the
// session in another folder.
func pinnedImportLaunchPath(workspace, resumeCwd string) (string, error) {
	if !pathWithin(workspace, resumeCwd) {
		return "", importNotice("this chat's folder is no longer inside its AO workspace")
	}
	if err := os.MkdirAll(resumeCwd, 0o700); err != nil {
		return "", err
	}
	return resumeCwd, nil
}

func sameDirectory(a, b string) bool {
	if resolved, err := filepath.EvalSymlinks(a); err == nil {
		a = resolved
	}
	if resolved, err := filepath.EvalSymlinks(b); err == nil {
		b = resolved
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// importOwnedElsewhere reports positive evidence that another provider process
// holds the conversation: Codex keeps a writer lock per loaded thread, Claude
// Code registers each live session, and older writers keep the transcript open.
//
// Pi has no such signal: it keeps no writer lock or live-session registry and
// does not hold its session file open (see writableTranscriptOwner), and
// pi-acp's session/load does not refuse a second writer. So a Pi conversation
// still open in the Pi CLI/TUI is not detected, and importing it can interleave
// turns with the other writer. This matches the documented posture for
// lock-less providers; a reliable per-session Pi owner signal would be needed
// to protect it.
func importOwnedElsewhere(ctx context.Context, rec domain.SessionRecord) bool {
	source := rec.Metadata.ImportSource
	switch rec.Harness {
	case domain.HarnessCodex:
		if providerLockHeld(filepath.Join(source.ConfigDir, "thread-writer-locks", source.NativeID+".lock")) {
			return true
		}
	case domain.HarnessClaudeCode:
		if claudeSessionLive(ctx, filepath.Join(source.ConfigDir, "sessions"), source.NativeID, rec.Metadata.WorkspacePath) {
			return true
		}
	case domain.HarnessKimi:
		// Kimi publishes a per-session lock file holding the live writer's pid;
		// its transcript is append-open-close so it is never held open for lsof.
		return kimiSessionLocked(ctx, source.ConfigDir, source.NativeID)
	case domain.HarnessOpenCode:
		// OpenCode keeps all conversations in one shared SQLite store with no
		// per-session lock or live-session registry, and its transcript path is a
		// row id rather than a writable file. With no positive evidence that
		// another process holds this conversation, report not-owned.
		return false
	}
	// A writer holds the plain file; Codex restores a compressed one on resume.
	return writableTranscriptOwner(ctx, strings.TrimSuffix(rec.Metadata.NativeTranscriptPath, ".zst"))
}

// adoptedOwnedElsewhere keeps the open-elsewhere rule after adoption without
// launching the provider: Claude Code would accept a second writer, and a Codex
// launch that Codex then refuses costs a full app-server start per retry.
func adoptedOwnedElsewhere(ctx context.Context, rec domain.SessionRecord) bool {
	source := rec.Metadata.ImportSource
	if source == nil {
		return false
	}
	nativeID := cmp.Or(rec.Metadata.ProviderConversationID, source.NativeID)
	switch rec.Harness {
	case domain.HarnessClaudeCode:
		return claudeSessionLive(ctx, filepath.Join(source.ConfigDir, "sessions"), nativeID, rec.Metadata.WorkspacePath)
	case domain.HarnessCodex:
		return providerLockHeld(filepath.Join(source.ConfigDir, "thread-writer-locks", nativeID+".lock"))
	case domain.HarnessKimi:
		return kimiSessionLocked(ctx, source.ConfigDir, nativeID)
	}
	return false
}

// claudeSessionLive reads Claude Code's live-session registry. A launch inside
// this session's own AO workspace is an earlier AO attempt, not another owner.
func claudeSessionLive(ctx context.Context, dir, nativeID, ownWorkspace string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		var live struct {
			PID       int    `json:"pid"`
			SessionID string `json:"sessionId"`
			CWD       string `json:"cwd"`
			StartedAt int64  `json:"startedAt"`
		}
		if err != nil || json.Unmarshal(body, &live) != nil || live.SessionID != nativeID || live.PID <= 0 {
			continue
		}
		if ownWorkspace != "" && pathWithin(ownWorkspace, live.CWD) {
			continue
		}
		if claudeProcessLive(ctx, live.PID, live.StartedAt) {
			return true
		}
	}
	return false
}

// claudeProcessLive checks a registry entry against the process now holding its
// PID. Registry files outlive crashes and PIDs are reused, so the PID must still
// be a Claude process, and one that started before it registered the session.
func claudeProcessLive(ctx context.Context, pid int, registeredAtMillis int64) bool {
	if !processalive.Alive(pid) {
		return false
	}
	name, started, ok := processStart(ctx, pid)
	name = strings.ToLower(name)
	if !ok || !strings.Contains(name, "claude") && !strings.Contains(name, "node") {
		return false
	}
	return registeredAtMillis <= 0 || !started.After(time.UnixMilli(registeredAtMillis))
}

// writableTranscriptOwner is the fallback for writers without a lock or registry.
// Read-only descriptors are not ownership.
//
// It cannot see a live Pi owner: the pi CLI/TUI and pi-acp persist with
// appendFileSync/writeFileSync, opening and closing the session file for each
// entry rather than holding it open, so lsof reports a Pi writer only during
// the instant of a write. Pi also keeps no writer lock or live-session
// registry, and pi-acp's session/load does not refuse a second writer, so an
// imported Pi conversation that is still open elsewhere is not detected. See
// importOwnedElsewhere.
func writableTranscriptOwner(ctx context.Context, path string) bool {
	if path == "" {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, _ := process.CommandContext(probeCtx, "lsof", "-F", "pcfa", "--", path).Output()
	command := ""
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "p") {
			command = ""
		}
		if strings.HasPrefix(line, "c") {
			command = strings.ToLower(line[1:])
		}
		if (line == "aw" || line == "au") && (strings.Contains(command, "claude") || strings.Contains(command, "codex") || strings.Contains(command, "droid")) {
			return true
		}
	}
	return false
}

// importCommit fetches unpublished commits into the registered repository so
// every later restore/kill still uses the ordinary workspace lifecycle.
func importCommit(ctx context.Context, repo, source, sha string) error {
	if _, err := importGit(ctx, repo, "cat-file", "-e", sha+"^{commit}"); err == nil {
		return nil
	}
	_, err := importGit(ctx, repo, "fetch", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules", "--", source, sha)
	return err
}

func importGit(ctx context.Context, path string, args ...string) ([]byte, error) {
	cmd := process.CommandContext(ctx, "git", append([]string{"-C", path}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("read source Git state: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func (m *Manager) prepareImportedWorkspace(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord) (domain.SessionRecord, error) {
	if importOwnedElsewhere(ctx, rec) {
		return rec, ErrSessionOpenElsewhere
	}
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return rec, ports.ErrChatUnsupported
	}
	source := rec.Metadata.ImportSource
	if prober, ok := agent.(ports.AgentNativeSessionProber); ok {
		available, err := prober.ProbeNativeSession(ctx, ports.NativeSessionRef{NativeSessionID: source.NativeID, ConfigDir: source.ConfigDir})
		if err != nil {
			return rec, err
		}
		if available == ports.NativeSessionAvailabilityUnavailable {
			return rec, fmt.Errorf("%w: %w", ports.ErrChatResumeFailed, importNotice("the original provider history is missing; restore it and retry"))
		}
	}
	release := m.acquireWorkspaceGate(rec.ProjectID)
	defer release()
	ws := ports.WorkspaceInfo{Path: rec.Metadata.WorkspacePath, RepoPath: rec.Metadata.WorkspaceRepoPath, Branch: rec.Metadata.Branch}
	next := *source
	// A marker travels with the directory swap, so a failed DB write cannot
	// make a successful copy run again over edits in the AO workspace.
	copied := source.Transferred || source.Prepared
	if !copied && ws.Path != "" {
		marker, err := os.ReadFile(importCopyMarker(ws.Path, rec.ID))
		if err == nil {
			if string(marker) != string(rec.ID) {
				return rec, importNotice("invalid workspace transfer marker")
			}
			copied = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return rec, err
		}
	}
	if !copied {
		created := ws.Path == ""
		copyFrom := source.CWD
		overrides := map[string]string{}
		branch := "ao/" + string(rec.ID) + "/import"
		var err error
		var workspaceProject *ports.WorkspaceProjectInfo
		var diffSHA, diffRef string
		switch projectKindForSession(project, rec.ProjectID) {
		case domain.ProjectKindSingleRepo:
			gitRoot := copyFrom
			if _, err := os.Stat(copyFrom); errors.Is(err, os.ErrNotExist) {
				// The conversation's folder is gone, for example a removed worktree:
				// continue from the project checkout, with nothing left to copy.
				m.logger.Info("imported session folder is gone; continuing from the project checkout", "sessionID", rec.ID)
				gitRoot, copyFrom = project.Path, ""
			}
			top, err := importGit(ctx, gitRoot, "rev-parse", "--show-toplevel")
			if err != nil {
				return rec, err
			}
			repoRoot := strings.TrimSpace(string(top))
			if copyFrom != "" {
				// Never fetch or copy an unrelated repository that sits inside the project folder.
				if !sessionimport.SameRepository(ctx, repoRoot, project.Path) {
					return rec, importNotice("the conversation's folder is not a checkout of this project's repository")
				}
				copyFrom = repoRoot
			}
			head, err := importGit(ctx, repoRoot, "rev-parse", "HEAD")
			if err != nil {
				return rec, err
			}
			sha := strings.TrimSpace(string(head))
			// The worktree belongs to the registered repository, never to a second
			// clone the conversation ran in: that clone only lends its commit.
			if err := importCommit(ctx, project.Path, repoRoot, sha); err != nil {
				return rec, err
			}
			if created {
				ws, err = m.workspace.Create(ctx, ports.WorkspaceConfig{ProjectID: rec.ProjectID, SessionID: rec.ID, Kind: rec.Kind, SessionPrefix: sessionPrefix(project), Branch: branch, FreshBranch: true, BaseRef: sha, StartRef: sha})
				if err != nil {
					return rec, err
				}
			}
			// Like a session started from a feature branch, show only this
			// session's own changes: compare with the commit the chat continued
			// from. A retry resets an existing workspace to the source's current
			// HEAD, so the base follows the commit transferred this time.
			if ws.Path != "" {
				diffSHA, diffRef = importDiffBase(ctx, ws.Path, sha, project.Config.WorktreeBaseBranch())
			}
		case domain.ProjectKindWorkspace:
			copyFrom = project.Path
			repos, err := m.store.ListWorkspaceRepos(ctx, project.ID)
			if err != nil {
				return rec, err
			}
			_, statErr := os.Stat(source.CWD)
			gone := errors.Is(statErr, os.ErrNotExist)
			if gone {
				// As for a single repository: continue from the registered
				// checkouts, with nothing left to copy.
				m.logger.Info("imported session folder is gone; continuing from the project checkout", "sessionID", rec.ID)
			} else if top, err := importGit(ctx, source.CWD, "rev-parse", "--show-toplevel"); err != nil {
				return rec, err
			} else if nativeRoot := strings.TrimSpace(string(top)); sessionimport.SameRepository(ctx, nativeRoot, project.Path) {
				copyFrom = nativeRoot
			} else {
				for _, repo := range repos {
					repoPath := filepath.Join(project.Path, filepath.FromSlash(repo.RelativePath))
					if sessionimport.SameRepository(ctx, nativeRoot, repoPath) {
						overrides[filepath.FromSlash(repo.RelativePath)] = nativeRoot
					}
				}
				if len(overrides) > 1 {
					return rec, importNotice("the original repository matches multiple workspace children")
				}
				if len(overrides) == 0 && !pathWithin(copyFrom, source.CWD) {
					return rec, importNotice("the original workspace no longer matches this AO project")
				}
			}
			baseRefs := map[string]string{}
			roots := map[string]string{filepath.Clean(project.Path): copyFrom}
			for _, repo := range repos {
				if repo.GitStatus == domain.GitStatusNeedsInit {
					continue
				}
				rel := filepath.FromSlash(repo.RelativePath)
				registered := filepath.Join(project.Path, rel)
				native := filepath.Join(copyFrom, rel)
				if override := overrides[rel]; override != "" {
					native = override
				} else if copyFrom != project.Path && !sessionimport.SameRepository(ctx, native, registered) {
					// A separate clone of the root holds no child checkouts: the
					// child starts from the registered one, with nothing copied.
					native = registered
					overrides[rel] = ""
				}
				roots[registered] = native
			}
			for repoPath, nativePath := range roots {
				head, err := importGit(ctx, nativePath, "rev-parse", "HEAD")
				if err != nil {
					return rec, err
				}
				sha := strings.TrimSpace(string(head))
				if err := importCommit(ctx, repoPath, nativePath, sha); err != nil {
					return rec, err
				}
				baseRefs[repoPath] = sha
			}
			if created {
				ws, workspaceProject, err = m.createSessionWorkspaceAt(ctx, project, ports.SpawnConfig{ProjectID: rec.ProjectID, Kind: rec.Kind}, rec.ID, branch, baseRefs, baseRefs[filepath.Clean(project.Path)])
				if err != nil {
					return rec, err
				}
			}
			if gone {
				copyFrom = ""
			}
		default:
			// A standalone chat's folder is the home or a provider scratch folder, never a tree to copy.
			copyFrom = ""
			if created {
				ws, _, err = m.createSessionWorkspace(ctx, project, ports.SpawnConfig{ProjectID: rec.ProjectID, Kind: rec.Kind}, rec.ID, branch, nil)
				if err != nil {
					return rec, err
				}
			}
		}
		if copyFrom != "" {
			// Compare physical paths: a project registered under a symlink, such
			// as /tmp on macOS, still contains the conversation's folder.
			physical := func(path string) string {
				if resolved, err := filepath.EvalSymlinks(path); err == nil {
					return resolved
				}
				return path
			}
			base, cwd := physical(copyFrom), physical(source.CWD)
			for rel, nativePath := range overrides {
				if nativePath == "" {
					continue
				}
				if nativePath = physical(nativePath); pathWithin(nativePath, cwd) {
					subdir, _ := filepath.Rel(nativePath, cwd)
					cwd = filepath.Join(base, rel, subdir)
					break
				}
			}
			if pathWithin(base, cwd) {
				next.WorkingSubdir, _ = filepath.Rel(base, cwd)
				if next.WorkingSubdir == "." {
					next.WorkingSubdir = ""
				}
			}
		}
		if created {
			// Publish the clean workspace before moving any source edits into it.
			workspace := rec.Metadata
			workspace.Branch, workspace.WorkspacePath, workspace.WorkspaceRepoPath = ws.Branch, ws.Path, ws.RepoPath
			if diffSHA != "" {
				workspace.DiffBaseSHA, workspace.DiffBaseRef = diffSHA, diffRef
			}
			updated, err := m.store.SetSessionImportWorkspace(ctx, rec.ID, source, &next, workspace, m.clock())
			if err != nil || !updated {
				cleanupCtx, cancel := spawnRollbackContext(ctx)
				defer cancel()
				var cleanupErr error
				if adapter, ok := m.workspace.(ports.WorkspaceProject); ok && workspaceProject != nil {
					cleanupErr = adapter.DestroyWorkspaceProject(cleanupCtx, *workspaceProject)
				} else {
					cleanupErr = m.workspace.Destroy(cleanupCtx, ws)
				}
				if err == nil {
					err = importNotice("session changed during workspace creation; retry")
				}
				return rec, errors.Join(err, cleanupErr)
			}
			stored := next
			workspace.ImportSource = &stored
			rec.Metadata = workspace
			source = rec.Metadata.ImportSource
		} else if diffSHA != "" && (rec.Metadata.DiffBaseSHA != diffSHA || rec.Metadata.DiffBaseRef != diffRef) {
			// Record the new base before the copy moves the workspace to it.
			workspace := rec.Metadata
			workspace.DiffBaseSHA, workspace.DiffBaseRef = diffSHA, diffRef
			updated, err := m.store.SetSessionImportWorkspace(ctx, rec.ID, source, source, workspace, m.clock())
			if err != nil {
				return rec, err
			}
			if !updated {
				return rec, importNotice("session changed during workspace transfer; retry")
			}
			rec.Metadata = workspace
		}
		if copyFrom != "" {
			if err := copyImportedWorkspace(ctx, copyFrom, ws.Path, overrides, importCopyMarker(ws.Path, rec.ID)); err != nil {
				return rec, fmt.Errorf("copy original files: %w", err)
			}
		}
	}
	if !source.Transferred {
		next.Transferred = true
		updated, err := m.store.SetSessionImportWorkspace(ctx, rec.ID, source, &next, rec.Metadata, m.clock())
		if err != nil {
			return rec, err
		}
		if !updated {
			return rec, importNotice("session changed during workspace transfer; retry")
		}
		rec.Metadata.ImportSource = &next
	}
	if err := os.Remove(importCopyMarker(ws.Path, rec.ID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return rec, err
	}
	// Retry setup after a failed post-create command, without copying over AO edits.
	if !rec.Metadata.ImportSource.Prepared {
		// Like a native spawn, give the post-create steps their own limits on
		// top of the resume request's budget, so a long install is not cut
		// short and rerun from the start on every retry.
		setupCtx, cancelSetup := ports.ExtendSpawnRequestBudget(ctx, postCreateBudget(0, project.Config.PostCreate))
		err := m.provisionWorkspace(setupCtx, project, ws.Path)
		cancelSetup()
		if err != nil {
			return rec, err
		}
		prepared := *rec.Metadata.ImportSource
		prepared.Prepared = true
		updated, err := m.store.SetSessionImportWorkspace(ctx, rec.ID, rec.Metadata.ImportSource, &prepared, rec.Metadata, m.clock())
		if err != nil {
			return rec, err
		}
		if !updated {
			return rec, importNotice("session changed during workspace setup; retry")
		}
	}
	current, ok, err := m.store.GetSession(ctx, rec.ID)
	if err != nil {
		return rec, err
	}
	if !ok || current.IsTerminated {
		return rec, importNotice("session was terminated during workspace setup")
	}
	return current, nil
}

// importDiffBase is the diff base for an imported workspace whose history ends
// at sha: the commit itself, named after the default branch while that commit
// is on it.
func importDiffBase(ctx context.Context, root, sha, defaultBranch string) (string, string) {
	for _, ref := range spawnDiffBaseRefCandidates(defaultBranch) {
		if base, ok := spawnGitSingleLine(ctx, root, "merge-base", sha, ref); ok {
			if base == sha {
				return sha, ref
			}
			break
		}
	}
	return sha, "HEAD"
}

// importLaunchPath is the folder an imported chat continues in: the subfolder
// it ran in when the new worktree has it. Only Git's changes are copied, so a
// git-ignored or empty folder, or one inside an unregistered nested repository,
// is missing there; the provider would then fail to start on every retry, so
// the chat continues from the worktree root instead.
func importLaunchPath(workspace, subdir string) string {
	path := filepath.Join(workspace, subdir)
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		return workspace
	}
	return path
}

// importHeldByLiveSession reports whether a terminated imported session's
// conversation was imported again into a live session. Only one live session
// may hold an imported conversation, so the terminated one cannot come back.
func (m *Manager) importHeldByLiveSession(ctx context.Context, rec domain.SessionRecord) (bool, error) {
	source := rec.Metadata.ImportSource
	if source == nil {
		return false, nil
	}
	sessions, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return false, err
	}
	for _, other := range sessions {
		if other.ID == rec.ID || other.IsTerminated || other.Metadata.ImportSource == nil {
			continue
		}
		if other.Metadata.ImportSource.ConfigDir == source.ConfigDir && other.Metadata.ImportSource.NativeID == source.NativeID {
			return true, nil
		}
	}
	return false, nil
}

// chatPermissionModeLister lists the permission modes a harness can run a chat
// in, strictest first.
type chatPermissionModeLister interface {
	PermissionModes(ctx context.Context, harness domain.AgentHarness) ([]ports.PermissionMode, error)
}

// importedChatPermissions is the permission mode an imported chat opens in. An
// import never chose one, so it would inherit the project default, which some
// harnesses cannot run: Pi cannot ask for approval, so it only runs bypassing
// permissions. The project default is kept when the harness accepts it;
// otherwise the strictest mode the harness accepts is used, as a new chat on
// that harness would offer.
func (m *Manager) importedChatPermissions(ctx context.Context, rec domain.SessionRecord, configured ports.PermissionMode) ports.PermissionMode {
	if rec.Metadata.ImportSource == nil || rec.Metadata.Permissions != "" {
		return configured
	}
	lister, ok := m.chat.(chatPermissionModeLister)
	if !ok {
		return configured
	}
	modes, err := lister.PermissionModes(ctx, rec.Harness)
	if err != nil || len(modes) == 0 {
		return configured
	}
	want := ports.NormalizePermissionMode(configured)
	for _, mode := range modes {
		if ports.NormalizePermissionMode(mode) == want {
			return configured
		}
	}
	return modes[0]
}

func importCopyMarker(workspace string, id domain.SessionID) string {
	return filepath.Join(workspace, ".ao-import-complete-"+string(id))
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

type importFileFact struct {
	Mode     fs.FileMode
	Size     int64
	Modified time.Time
}

// importChanges lists what Git shows as differing from HEAD: tracked edits and
// deletions, staged or not, plus untracked files that are not ignored. Ignored
// output such as node_modules, and nested repositories such as Claude's agent
// worktrees, stay behind; the project's symlinks and setup provide them, as for
// a native session. It reads without taking the source's index lock.
func importChanges(ctx context.Context, repo string) ([]string, error) {
	tracked, err := importGit(ctx, repo, "--no-optional-locks", "diff", "--name-only", "-z", "--no-renames", "HEAD")
	if err != nil {
		return nil, err
	}
	untracked, err := importGit(ctx, repo, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, path := range strings.Split(string(tracked)+string(untracked), "\x00") {
		// A nested repository is listed as a directory.
		if path != "" && !strings.HasSuffix(path, "/") {
			paths = append(paths, filepath.FromSlash(path))
		}
	}
	slices.Sort(paths)
	return slices.Compact(paths), nil
}

// importFacts records each changed path as it is now; a path missing from the
// result was deleted.
func importFacts(ctx context.Context, root *os.Root, paths []string) (map[string]importFileFact, error) {
	facts := map[string]importFileFact{}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := root.Lstat(path)
		if errors.Is(err, os.ErrNotExist) || err != nil && importAncestorNotDir(root, path) {
			// A tracked folder replaced by a file (ENOTDIR on its children)
			// deleted everything that was under it.
			continue
		}
		if err != nil {
			return nil, err
		}
		switch {
		case info.IsDir():
			facts[path] = importFileFact{Mode: info.Mode()} // A submodule keeps its folder, as in a fresh checkout.
		case info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0:
			facts[path] = importFileFact{Mode: info.Mode(), Size: info.Size(), Modified: info.ModTime()}
		}
	}
	return facts, nil
}

// importAncestorNotDir reports whether a folder above path now exists as
// something other than a folder, so path itself cannot exist.
func importAncestorNotDir(root *os.Root, path string) bool {
	for dir := filepath.Dir(path); dir != "." && dir != string(filepath.Separator); dir = filepath.Dir(dir) {
		if info, err := root.Lstat(dir); err == nil {
			return !info.IsDir()
		}
	}
	return false
}

// clearEarlierImportCopy removes what an earlier, failed copy left that the
// source no longer shows. The reset restored tracked files; untracked ones stay
// unless removed here. Only paths that attempt recorded are touched, so setup
// output and nested repositories are left alone. The record lives in the
// worktree's own Git folder and is replaced by this attempt's paths.
func clearEarlierImportCopy(ctx context.Context, target string, dst *os.Root, changed []string) error {
	gitDir, err := importGit(ctx, target, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return err
	}
	record := filepath.Join(strings.TrimSpace(string(gitDir)), "ao-import-copied")
	if previous, err := os.ReadFile(record); err == nil {
		earlier := map[string]bool{}
		for _, path := range strings.Split(string(previous), "\x00") {
			earlier[path] = path != ""
		}
		for _, path := range changed {
			delete(earlier, path)
		}
		untracked, err := importGit(ctx, target, "ls-files", "-z", "--others", "--exclude-standard")
		if err != nil {
			return err
		}
		for _, path := range strings.Split(string(untracked), "\x00") {
			if path = filepath.FromSlash(path); earlier[path] {
				if err := dst.RemoveAll(path); err != nil {
					return err
				}
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(record, []byte(strings.Join(changed, "\x00")), 0o600)
}

func importGitPaths(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Name() != ".git" {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return importNotice("git metadata must not be a symlink")
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.Dir(rel))
		if entry.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	return paths, err
}

// copyImportedWorkspace moves the original checkout's uncommitted state into
// the AO worktree in place. Each repository is reset to the source's current
// HEAD (the worktree has not been handed to an agent yet, and a retry follows a
// source that moved on), then only what Git shows as changed is copied and the
// source index is replayed. Ignored files, nested repositories and whatever the
// workspace setup added stay as they are, and the source is only read. An
// override names another checkout of one repository; an empty one leaves that
// repository's fresh checkout as it is.
func copyImportedWorkspace(ctx context.Context, source, destination string, overrides map[string]string, completionMarker string) error {
	source, err := filepath.EvalSymlinks(source)
	if err != nil {
		return err
	}
	if pathWithin(source, destination) {
		return importNotice("destination must be outside the original folder")
	}
	gitPaths, err := importGitPaths(destination)
	if err != nil {
		return err
	}
	type gitState struct {
		rel, source  string
		head, staged []byte
		changed      []string
		facts        map[string]importFileFact
	}
	read := func(state *gitState) error {
		head, err := importGit(ctx, state.source, "rev-parse", "HEAD")
		if err != nil {
			return err
		}
		staged, err := importGit(ctx, state.source, "diff", "--cached", "--binary", "--full-index", "HEAD")
		if err != nil {
			return err
		}
		changed, err := importChanges(ctx, state.source)
		if err != nil {
			return err
		}
		root, err := os.OpenRoot(state.source)
		if err != nil {
			return err
		}
		facts, err := importFacts(ctx, root, changed)
		_ = root.Close()
		state.head, state.staged, state.changed, state.facts = head, staged, changed, facts
		return err
	}
	states := make([]gitState, 0, len(gitPaths))
	for _, rel := range gitPaths {
		native := filepath.Join(source, rel)
		if override, ok := overrides[rel]; ok {
			if override == "" {
				continue
			}
			native = override
		}
		if pathWithin(native, destination) {
			return importNotice("destination must be outside the original folder")
		}
		state := gitState{rel: rel, source: native}
		if err := read(&state); err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		sha := strings.TrimSpace(string(state.head))
		if err := importCommit(ctx, target, native, sha); err != nil {
			return err
		}
		if _, err := importGit(ctx, target, "reset", "--quiet", "--hard", sha); err != nil {
			return err
		}
		states = append(states, state)
	}
	linkRoots := map[string]string{source: ""}
	for rel, original := range overrides {
		if original == "" {
			continue
		}
		resolved, err := filepath.EvalSymlinks(original)
		if err != nil {
			return err
		}
		linkRoots[resolved] = rel
	}
	copyFile := func(src, dst *os.Root, rel, path string, fact importFileFact) error {
		if fact.Mode&os.ModeSymlink != 0 {
			link, err := src.Readlink(path)
			if err != nil {
				return err
			}
			if filepath.IsAbs(link) {
				// Rebase links into any copied tree, including a separately cloned child.
				physical := link
				if parent, err := filepath.EvalSymlinks(filepath.Dir(link)); err == nil {
					physical = filepath.Join(parent, filepath.Base(link))
				}
				best := ""
				for original := range linkRoots {
					if pathWithin(original, physical) && len(original) > len(best) {
						best = original
					}
				}
				if best != "" {
					inside, _ := filepath.Rel(best, physical)
					link, err = filepath.Rel(filepath.Dir(filepath.Join(destination, rel, path)), filepath.Join(destination, linkRoots[best], inside))
					if err != nil {
						return err
					}
				}
			}
			return dst.Symlink(link, path)
		}
		in, err := src.Open(path)
		if err != nil {
			return err
		}
		out, err := dst.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fact.Mode.Perm())
		if err != nil {
			_ = in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		_ = in.Close()
		if err := errors.Join(copyErr, out.Close()); err != nil {
			return err
		}
		return dst.Chtimes(path, fact.Modified, fact.Modified)
	}
	for _, state := range states {
		src, err := os.OpenRoot(state.source)
		if err != nil {
			return err
		}
		dst, err := os.OpenRoot(filepath.Join(destination, state.rel))
		if err != nil {
			_ = src.Close()
			return err
		}
		if err := clearEarlierImportCopy(ctx, filepath.Join(destination, state.rel), dst, state.changed); err != nil {
			_ = src.Close()
			_ = dst.Close()
			return err
		}
		// Deletions first, then parents before children, so a file and a folder
		// can trade places.
		paths := slices.Clone(state.changed)
		slices.SortStableFunc(paths, func(a, b string) int {
			_, aKept := state.facts[a]
			_, bKept := state.facts[b]
			if aKept == bKept {
				return 0
			}
			if aKept {
				return 1
			}
			return -1
		})
		for _, path := range paths {
			fact, kept := state.facts[path]
			switch {
			case !kept:
				err = dst.RemoveAll(path)
			case fact.Mode.IsDir():
				if info, statErr := dst.Lstat(path); statErr == nil && !info.IsDir() {
					err = dst.Remove(path)
				}
				if err == nil {
					err = dst.MkdirAll(path, 0o755)
				}
			default:
				if err = dst.RemoveAll(path); err == nil {
					if err = dst.MkdirAll(filepath.Dir(path), 0o755); err == nil {
						err = copyFile(src, dst, state.rel, path, fact)
					}
				}
			}
			if err != nil {
				break
			}
		}
		_ = src.Close()
		_ = dst.Close()
		if err != nil {
			return err
		}
	}
	for _, state := range states {
		if len(state.staged) == 0 {
			continue
		}
		cmd := process.CommandContext(ctx, "git", "-C", filepath.Join(destination, state.rel), "apply", "--cached", "--binary", "-")
		cmd.Stdin = bytes.NewReader(state.staged)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("preserve staged edits: %w: %s", err, out)
		}
	}
	for _, before := range states {
		after := gitState{source: before.source}
		if err := read(&after); err != nil {
			return err
		}
		if !bytes.Equal(before.head, after.head) || !bytes.Equal(before.staged, after.staged) ||
			!slices.Equal(before.changed, after.changed) || !maps.Equal(before.facts, after.facts) {
			return importNotice("source files changed while copying; retry")
		}
	}
	if completionMarker == "" {
		return nil
	}
	if _, err := os.Lstat(completionMarker); !errors.Is(err, os.ErrNotExist) {
		return importNotice("source contains a reserved workspace transfer marker")
	}
	return os.WriteFile(completionMarker, []byte(strings.TrimPrefix(filepath.Base(completionMarker), ".ao-import-complete-")), 0o600)
}

func providerRefusedImportOwnership(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, refusal := range []string{"this is open in another app", "session is already in use", "thread is already in use", "already has an active writer", "session is locked by another process"} {
		if strings.Contains(message, refusal) {
			return true
		}
	}
	return false
}
