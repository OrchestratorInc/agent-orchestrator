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

	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionimport"
)

type ImportArchiveRefresher interface {
	RefreshArchive(context.Context, domain.SessionRecord) error
}

func (m *Manager) SetImportArchiveRefresher(refresher ImportArchiveRefresher) {
	m.importArchive = refresher
}

func (m *Manager) refreshImportArchive(ctx context.Context, rec domain.SessionRecord) {
	if m.importArchive == nil {
		return
	}
	if err := m.importArchive.RefreshArchive(ctx, rec); err != nil {
		m.logger.Warn("imported history refresh failed; keeping the import-time copy", "sessionID", rec.ID, "error", err)
	}
}

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

var ErrSessionOpenElsewhere = errors.New("this is open elsewhere; close it there to continue here")

func (m *Manager) applyImportedConfigEnv(ctx context.Context, rec domain.SessionRecord, env map[string]string) {
	source := rec.Metadata.ImportSource
	if source == nil {
		return
	}
	if model := strings.TrimSpace(rec.Metadata.Model); rec.Harness == domain.HarnessClaudeCode && model != "" && model != "default" && strings.TrimSpace(os.Getenv("ANTHROPIC_MODEL")) == "" {
		if _, set := env["ANTHROPIC_MODEL"]; !set {
			env["ANTHROPIC_MODEL"] = model
		}
	}
	ref := importedNativeRef(source)
	if rec.Metadata.ProviderConversationID != ref.NativeSessionID {
		return
	}
	hooks := importedHarnesses[rec.Harness]
	key := hooks.configEnvKey
	if key == "" {
		return
	}
	if rec.Harness == domain.HarnessKimchi {
		setProtectedEnv(env, key, source.ConfigDir, envKeysCaseInsensitive)
		return
	}
	if agent, ok := m.agents.Agent(rec.Harness); ok {
		if provider, ok := agent.(ports.AgentNativeSessionConfigProvider); ok {
			if current, err := provider.NativeSessionConfigDir(ctx, env); err == nil {
				if sameDirectory(current, ref.ConfigDir) {
					return
				}
				if rel, err := filepath.Rel(ref.ConfigDir, rec.Metadata.NativeTranscriptPath); err == nil && filepath.IsLocal(rel) {
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
	value := ref.ConfigDir
	if hooks.configValue != nil {
		value = hooks.configValue(ref.ConfigDir)
	}
	setProtectedEnv(env, key, value, envKeysCaseInsensitive)
}

func (m *Manager) relocateImportedSession(ctx context.Context, rec domain.SessionRecord, launchPath string, env map[string]string) (domain.SessionRecord, error) {
	source := rec.Metadata.ImportSource
	if source == nil || m.agents == nil {
		return rec, nil
	}
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return rec, nil
	}
	if preparer, ok := agent.(ports.AgentImportedSessionPreparer); ok {
		prepared, err := m.prepareImportedNativeSession(ctx, rec, launchPath, preparer, env)
		return prepared, agentlaunch.RedactError(err, env)
	}
	relocator, ok := agent.(ports.AgentImportedSessionRelocator)
	if !ok {
		return rec, nil
	}
	err := relocator.RelocateImportedSession(ctx, ports.ImportedSessionRelocation{
		NativeSessionID:   source.NativeID,
		ConfigDir:         source.ConfigDir,
		TranscriptPath:    rec.Metadata.NativeTranscriptPath,
		SourceCWD:         source.CWD,
		LaunchCWD:         launchPath,
		Chat:              domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat,
		SourceFingerprint: source.Fingerprint,
		DataDir:           m.dataDir,
		Env:               maps.Clone(env),
	})
	if err != nil {
		return rec, fmt.Errorf("%w: %w", importNotice("Orchestrator could not prepare this conversation for its workspace. See the Orchestrator daemon log for details."), agentlaunch.RedactError(err, env))
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

func (m *Manager) importLaunch(ctx context.Context, operation string, rec domain.SessionRecord, workspace string, env map[string]string, prepare bool) (domain.SessionRecord, string, error) {
	source := rec.Metadata.ImportSource
	if source == nil {
		return rec, workspace, nil
	}
	launchPath := workspace
	if source.ResumeCwd != "" {
		pinned, err := pinnedImportLaunchPath(workspace, source.ResumeCwd)
		if err != nil {
			return rec, "", relaunchFailed("import launch directory", fmt.Errorf("%s %s: %w", operation, rec.ID, err))
		}
		launchPath = pinned
	} else if source.WorkingSubdir != "" {
		launchPath = importLaunchPath(workspace, source.WorkingSubdir)
	}
	if !prepare {
		return rec, launchPath, nil
	}
	rec, err := m.relocateImportedSession(ctx, rec, launchPath, env)
	if err != nil {
		return rec, "", relaunchFailed("import relocation", fmt.Errorf("%s %s: %w", operation, rec.ID, err))
	}
	return rec, launchPath, nil
}

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

func importOwnedElsewhere(ctx context.Context, rec domain.SessionRecord) bool {
	source := rec.Metadata.ImportSource
	if source == nil {
		return false
	}
	if hooks := importedHarnesses[rec.Harness]; hooks.ownedElsewhere != nil {
		return hooks.ownedElsewhere(ctx, rec)
	}
	switch rec.Harness {
	case domain.HarnessCodex:
		if providerLockHeld(filepath.Join(source.ConfigDir, "thread-writer-locks", source.NativeID+".lock")) {
			return true
		}
	case domain.HarnessClaudeCode:
		if claudeSessionLive(ctx, filepath.Join(source.ConfigDir, "sessions"), source.NativeID, rec.Metadata.WorkspacePath) {
			return true
		}
	case domain.HarnessQwen:
		return qwenSessionLive(ctx, source.ConfigDir, source.NativeID, rec.Metadata.WorkspacePath)
	case domain.HarnessOMP:
		if ompSessionLocked(ctx, source.ConfigDir, source.NativeID) {
			return true
		}
	case domain.HarnessKimchi:
		return false
	case domain.HarnessOpenCode:
		return false
	}
	return writableTranscriptOwner(ctx, strings.TrimSuffix(rec.Metadata.NativeTranscriptPath, ".zst"))
}

func adoptedOwnedElsewhere(ctx context.Context, rec domain.SessionRecord) bool {
	source := rec.Metadata.ImportSource
	if source == nil {
		return false
	}
	if hooks := importedHarnesses[rec.Harness]; hooks.adoptedOwnedElsewhere != nil {
		return hooks.adoptedOwnedElsewhere(ctx, rec)
	}
	nativeID := cmp.Or(rec.Metadata.ProviderConversationID, source.NativeID)
	switch rec.Harness {
	case domain.HarnessClaudeCode:
		return claudeSessionLive(ctx, filepath.Join(source.ConfigDir, "sessions"), nativeID, rec.Metadata.WorkspacePath)
	case domain.HarnessCodex:
		return providerLockHeld(filepath.Join(source.ConfigDir, "thread-writer-locks", nativeID+".lock"))
	case domain.HarnessQwen:
		return qwenSessionLive(ctx, source.ConfigDir, nativeID, rec.Metadata.WorkspacePath)
	case domain.HarnessCopilot:
		return writableTranscriptOwner(ctx, rec.Metadata.NativeTranscriptPath)
	case domain.HarnessOMP:
		return ompSessionLocked(ctx, source.ConfigDir, nativeID)
	}
	return false
}

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
		if line == "aw" || line == "au" {
			for _, name := range importedProcessNames {
				if strings.Contains(command, name) {
					return true
				}
			}
		}
	}
	return false
}

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
		return rec, fmt.Errorf("no agent adapter for harness %q", rec.Harness)
	}
	source := rec.Metadata.ImportSource
	deferSourceProbe := source.Prepared && rec.Metadata.WorkspacePath != "" &&
		source.ResumeNativeID == "" && source.ResumeCwd == "" && m.hasFingerprintImportPreparer(rec)
	if deferSourceProbe {
		workspace, statErr := os.Stat(rec.Metadata.WorkspacePath)
		deferSourceProbe = statErr == nil && workspace.IsDir()
	}
	if prober, ok := agent.(ports.AgentNativeSessionProber); ok && !deferSourceProbe {
		available, err := prober.ProbeNativeSession(ctx, importedNativeRef(source))
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
				m.logger.Info("imported session folder is gone; continuing from the project checkout", "sessionID", rec.ID)
				gitRoot, copyFrom = project.Path, ""
			}
			top, err := importGit(ctx, gitRoot, "rev-parse", "--show-toplevel")
			if err != nil {
				return rec, err
			}
			repoRoot := strings.TrimSpace(string(top))
			if copyFrom != "" {
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
			if err := importCommit(ctx, project.Path, repoRoot, sha); err != nil {
				return rec, err
			}
			if created {
				ws, err = m.workspace.Create(ctx, ports.WorkspaceConfig{ProjectID: rec.ProjectID, SessionID: rec.ID, Kind: rec.Kind, SessionPrefix: sessionPrefix(project), Branch: branch, FreshBranch: true, BaseRef: sha, StartRef: sha})
				if err != nil {
					return rec, err
				}
			}
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
			copyFrom = ""
			if created {
				ws, _, err = m.createSessionWorkspace(ctx, project, ports.SpawnConfig{ProjectID: rec.ProjectID, Kind: rec.Kind}, rec.ID, branch, nil)
				if err != nil {
					return rec, err
				}
			}
		}
		if copyFrom != "" {
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
	if !rec.Metadata.ImportSource.Prepared {
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

func importLaunchPath(workspace, subdir string) string {
	path := filepath.Join(workspace, subdir)
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		return workspace
	}
	return path
}

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

type chatPermissionModeLister interface {
	PermissionModes(ctx context.Context, harness domain.AgentHarness) ([]ports.PermissionMode, error)
}

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
		if path != "" && !strings.HasSuffix(path, "/") {
			paths = append(paths, filepath.FromSlash(path))
		}
	}
	slices.Sort(paths)
	return slices.Compact(paths), nil
}

func importFacts(ctx context.Context, root *os.Root, paths []string) (map[string]importFileFact, error) {
	facts := map[string]importFileFact{}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := root.Lstat(path)
		if errors.Is(err, os.ErrNotExist) || err != nil && importAncestorNotDir(root, path) {
			continue
		}
		if err != nil {
			return nil, err
		}
		switch {
		case info.IsDir():
			facts[path] = importFileFact{Mode: info.Mode()}
		case info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0:
			facts[path] = importFileFact{Mode: info.Mode(), Size: info.Size(), Modified: info.ModTime()}
		}
	}
	return facts, nil
}

func importAncestorNotDir(root *os.Root, path string) bool {
	for dir := filepath.Dir(path); dir != "." && dir != string(filepath.Separator); dir = filepath.Dir(dir) {
		if info, err := root.Lstat(dir); err == nil {
			return !info.IsDir()
		}
	}
	return false
}

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
