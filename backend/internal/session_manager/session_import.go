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
func (m *Manager) applyImportedConfigEnv(ctx context.Context, rec domain.SessionRecord, env map[string]string) {
	source := rec.Metadata.ImportSource
	if source == nil || rec.Metadata.ProviderConversationID != source.NativeID {
		return
	}
	key := map[domain.AgentHarness]string{domain.HarnessClaudeCode: "CLAUDE_CONFIG_DIR", domain.HarnessCodex: "CODEX_HOME"}[rec.Harness]
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
					if _, err := os.Stat(filepath.Join(current, rel)); err == nil {
						return
					}
				}
			}
		}
	}
	setProtectedEnv(env, key, source.ConfigDir, envKeysCaseInsensitive)
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
	}
	return writableTranscriptOwner(ctx, rec.Metadata.NativeTranscriptPath)
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
		if (line == "aw" || line == "au") && (strings.Contains(command, "claude") || strings.Contains(command, "codex")) {
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
			return rec, fmt.Errorf("%w: the original provider history is missing; restore it and retry", ports.ErrChatResumeFailed)
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
				return rec, errors.New("invalid workspace transfer marker")
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
					return rec, errors.New("the conversation's folder is not a checkout of this project's repository")
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
		case domain.ProjectKindWorkspace:
			if _, err := os.Stat(source.CWD); errors.Is(err, os.ErrNotExist) {
				return rec, fmt.Errorf("the conversation's folder %s no longer exists; restore it to continue here", source.CWD)
			}
			copyFrom = project.Path
			repos, err := m.store.ListWorkspaceRepos(ctx, project.ID)
			if err != nil {
				return rec, err
			}
			top, err := importGit(ctx, source.CWD, "rev-parse", "--show-toplevel")
			if err != nil {
				return rec, err
			}
			nativeRoot := strings.TrimSpace(string(top))
			if sessionimport.SameRepository(ctx, nativeRoot, project.Path) {
				copyFrom = nativeRoot
			} else {
				for _, repo := range repos {
					repoPath := filepath.Join(project.Path, filepath.FromSlash(repo.RelativePath))
					if sessionimport.SameRepository(ctx, nativeRoot, repoPath) {
						overrides[filepath.FromSlash(repo.RelativePath)] = nativeRoot
					}
				}
				if len(overrides) > 1 {
					return rec, errors.New("the original repository matches multiple workspace children")
				}
				if len(overrides) == 0 && !pathWithin(copyFrom, source.CWD) {
					return rec, errors.New("the original workspace no longer matches this AO project")
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
			updated, err := m.store.SetSessionImportWorkspace(ctx, rec.ID, source, &next, ws.Branch, ws.Path, ws.RepoPath, m.clock())
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
					err = errors.New("session changed during workspace creation; retry")
				}
				return rec, errors.Join(err, cleanupErr)
			}
			stored := next
			rec.Metadata.ImportSource = &stored
			rec.Metadata.WorkspacePath, rec.Metadata.WorkspaceRepoPath, rec.Metadata.Branch = ws.Path, ws.RepoPath, ws.Branch
			source = rec.Metadata.ImportSource
		}
		if copyFrom != "" {
			if err := copyImportedWorkspace(ctx, copyFrom, ws.Path, overrides, importCopyMarker(ws.Path, rec.ID)); err != nil {
				return rec, fmt.Errorf("copy original files: %w", err)
			}
		}
	}
	if !source.Transferred {
		next.Transferred = true
		updated, err := m.store.SetSessionImportWorkspace(ctx, rec.ID, source, &next, ws.Branch, ws.Path, ws.RepoPath, m.clock())
		if err != nil {
			return rec, err
		}
		if !updated {
			return rec, errors.New("session changed during workspace transfer; retry")
		}
		rec.Metadata.ImportSource = &next
	}
	if err := os.Remove(importCopyMarker(ws.Path, rec.ID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return rec, err
	}
	// Retry setup after a failed post-create command, without copying over AO edits.
	if !rec.Metadata.ImportSource.Prepared {
		if err := m.provisionWorkspace(ctx, project, ws.Path); err != nil {
			return rec, err
		}
		prepared := *rec.Metadata.ImportSource
		prepared.Prepared = true
		updated, err := m.store.SetSessionImportWorkspace(ctx, rec.ID, rec.Metadata.ImportSource, &prepared, ws.Branch, ws.Path, ws.RepoPath, m.clock())
		if err != nil {
			return rec, err
		}
		if !updated {
			return rec, errors.New("session changed during workspace setup; retry")
		}
	}
	current, ok, err := m.store.GetSession(ctx, rec.ID)
	if err != nil {
		return rec, err
	}
	if !ok || current.IsTerminated {
		return rec, errors.New("session was terminated during workspace setup")
	}
	return current, nil
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
		if errors.Is(err, os.ErrNotExist) {
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
			return errors.New("git metadata must not be a symlink")
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
		return errors.New("destination must be outside the original folder")
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
			return errors.New("destination must be outside the original folder")
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
			return errors.New("source files changed while copying; retry")
		}
	}
	if completionMarker == "" {
		return nil
	}
	if _, err := os.Lstat(completionMarker); !errors.Is(err, os.ErrNotExist) {
		return errors.New("source contains a reserved workspace transfer marker")
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
