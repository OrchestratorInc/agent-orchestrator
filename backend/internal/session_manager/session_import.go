package sessionmanager

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionimport"
)

// ErrSessionOpenElsewhere requires the other writer to close; AO never takes it over.
var ErrSessionOpenElsewhere = errors.New("this is open elsewhere; close it there to continue here")

func applyImportedConfigEnv(rec domain.SessionRecord, env map[string]string) {
	source := rec.Metadata.ImportSource
	if source == nil || rec.Metadata.ProviderConversationID != source.NativeID {
		return
	}
	if rec.Harness == domain.HarnessClaudeCode {
		env["CLAUDE_CONFIG_DIR"] = source.ConfigDir
	}
	if rec.Harness == domain.HarnessCodex {
		env["CODEX_HOME"] = source.ConfigDir
	}
}

// Positive evidence only: read-only descriptors are not ownership. Providers
// without inspectable file handles still arbitrate ownership through native Resume.
// ponytail: lsof misses owners without a writable handle; add provider lease probes when exposed.
func writableTranscriptOwner(ctx context.Context, path string) bool {
	if path == "" {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(probeCtx, "lsof", "-F", "pcfa", "--", path).Output()
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

func importGit(ctx context.Context, path string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", path}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("read source Git state: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func (m *Manager) prepareImportedWorkspace(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord) (domain.SessionRecord, error) {
	if writableTranscriptOwner(ctx, rec.Metadata.NativeTranscriptPath) {
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
			top, err := importGit(ctx, copyFrom, "rev-parse", "--show-toplevel")
			if err != nil {
				return rec, err
			}
			copyFrom = strings.TrimSpace(string(top))
			head, err := importGit(ctx, copyFrom, "rev-parse", "HEAD")
			if err != nil {
				return rec, err
			}
			if created {
				ws, err = m.workspace.Create(ctx, ports.WorkspaceConfig{ProjectID: rec.ProjectID, SessionID: rec.ID, Kind: rec.Kind, SessionPrefix: sessionPrefix(project), Branch: branch, FreshBranch: true, BaseRef: strings.TrimSpace(string(head)), RepoPath: copyFrom})
				if err != nil {
					return rec, err
				}
			}
		case domain.ProjectKindWorkspace:
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
				roots[filepath.Join(project.Path, rel)] = filepath.Join(copyFrom, rel)
				if override := overrides[rel]; override != "" {
					roots[filepath.Join(project.Path, rel)] = override
				}
			}
			for repoPath, nativePath := range roots {
				head, err := importGit(ctx, nativePath, "rev-parse", "HEAD")
				if err != nil {
					return rec, err
				}
				sha := strings.TrimSpace(string(head))
				// Import unpublished commits into the registered repository so every
				// later restore/kill still uses the ordinary workspace lifecycle.
				if _, err := importGit(ctx, repoPath, "cat-file", "-e", sha+"^{commit}"); err != nil {
					if _, err := importGit(ctx, repoPath, "fetch", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules", "--", nativePath, sha); err != nil {
						return rec, err
					}
				}
				baseRefs[repoPath] = sha
			}
			if created {
				ws, workspaceProject, err = m.createSessionWorkspace(ctx, project, ports.SpawnConfig{ProjectID: rec.ProjectID, Kind: rec.Kind}, rec.ID, branch, baseRefs)
				if err != nil {
					return rec, err
				}
			}
		default:
			if created {
				ws, _, err = m.createSessionWorkspace(ctx, project, ports.SpawnConfig{ProjectID: rec.ProjectID, Kind: rec.Kind}, rec.ID, branch, nil)
				if err != nil {
					return rec, err
				}
			}
		}
		if copyFrom != "" {
			cwd := source.CWD
			if physical, err := filepath.EvalSymlinks(cwd); err == nil {
				cwd = physical
			}
			for rel, nativePath := range overrides {
				if pathWithin(nativePath, cwd) {
					subdir, _ := filepath.Rel(nativePath, cwd)
					cwd = filepath.Join(copyFrom, rel, subdir)
					break
				}
			}
			if pathWithin(copyFrom, cwd) {
				next.WorkingSubdir, _ = filepath.Rel(copyFrom, cwd)
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

func importManifest(ctx context.Context, root *os.Root) (map[string]importFileFact, error) {
	facts := map[string]importFileFact{}
	err := fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Name() == ".git" {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if path == "." {
			return nil
		}
		info, err := root.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() && !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("cannot copy special file %s", path)
		}
		facts[path] = importFileFact{Mode: info.Mode(), Size: info.Size(), Modified: info.ModTime()}
		return nil
	})
	return facts, err
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

// Stage all files first. Preserve every AO Git pointer, HEAD and index separately,
// so nested repositories never acquire the source's administrative metadata.
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
	nativePaths, err := importGitPaths(source)
	if err != nil {
		return err
	}
	for _, rel := range nativePaths {
		if !slices.Contains(gitPaths, rel) {
			return fmt.Errorf("nested repository %s is not registered in this AO workspace", rel)
		}
	}
	for rel, native := range overrides {
		paths, err := importGitPaths(native)
		if err != nil {
			return err
		}
		for _, child := range paths {
			if path := filepath.Join(rel, child); !slices.Contains(gitPaths, path) {
				return fmt.Errorf("nested repository %s is not registered in this AO workspace", path)
			}
		}
	}
	type gitState struct {
		rel, source  string
		head, staged []byte
	}
	states := make([]gitState, 0, len(gitPaths))
	for _, rel := range gitPaths {
		native := filepath.Join(source, rel)
		if override := overrides[rel]; override != "" {
			native = override
		}
		if pathWithin(native, destination) {
			return errors.New("destination must be outside the original folder")
		}
		head, err := importGit(ctx, native, "rev-parse", "HEAD")
		if err != nil {
			return err
		}
		targetHead, err := importGit(ctx, filepath.Join(destination, rel), "rev-parse", "HEAD")
		if err != nil || !bytes.Equal(head, targetHead) {
			return errors.New("source HEAD changed before copying, or is unavailable in AO; retry")
		}
		staged, err := importGit(ctx, native, "diff", "--cached", "--binary", "--full-index", "HEAD")
		if err != nil {
			return err
		}
		states = append(states, gitState{rel: rel, source: native, head: head, staged: staged})
	}
	temp, err := os.MkdirTemp(filepath.Dir(destination), ".import-staging-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(temp) }()
	type snapshot struct {
		path  string
		facts map[string]importFileFact
	}
	var snapshots []snapshot
	linkRoots := map[string]string{source: ""}
	for rel, original := range overrides {
		resolved, err := filepath.EvalSymlinks(original)
		if err != nil {
			return err
		}
		linkRoots[resolved] = rel
	}
	copyTree := func(native, target string) error {
		src, err := os.OpenRoot(native)
		if err != nil {
			return err
		}
		defer func() { _ = src.Close() }()
		facts, err := importManifest(ctx, src)
		if err != nil {
			return err
		}
		snapshots = append(snapshots, snapshot{native, facts})
		if err := os.MkdirAll(target, 0o700); err != nil {
			return err
		}
		dst, err := os.OpenRoot(target)
		if err != nil {
			return err
		}
		defer func() { _ = dst.Close() }()
		return fs.WalkDir(src.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.Name() == ".git" {
				if entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if path == "." {
				return nil
			}
			fact, ok := facts[path]
			if !ok {
				return errors.New("source files changed while copying; retry")
			}
			if entry.IsDir() {
				return dst.MkdirAll(path, fact.Mode.Perm()|0o700)
			}
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
						rel, _ := filepath.Rel(best, physical)
						targetRel, _ := filepath.Rel(temp, target)
						link, err = filepath.Rel(filepath.Dir(filepath.Join(destination, targetRel, path)), filepath.Join(destination, linkRoots[best], rel))
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
		})
	}
	if err := copyTree(source, temp); err != nil {
		return err
	}
	for rel, native := range overrides {
		if _, err := safeRelPath(rel); err != nil {
			return err
		}
		target := filepath.Join(temp, rel)
		if err := os.RemoveAll(target); err != nil {
			return err
		}
		if err := copyTree(native, target); err != nil {
			return err
		}
	}
	for _, snapshot := range snapshots {
		root, err := os.OpenRoot(snapshot.path)
		if err != nil {
			return err
		}
		after, err := importManifest(ctx, root)
		_ = root.Close()
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(snapshot.facts, after) {
			return errors.New("source files changed while copying; retry")
		}
	}
	for _, state := range states {
		head, err := importGit(ctx, state.source, "rev-parse", "HEAD")
		if err != nil || !bytes.Equal(state.head, head) {
			return errors.New("source HEAD changed while copying; retry")
		}
		staged, err := importGit(ctx, state.source, "diff", "--cached", "--binary", "--full-index", "HEAD")
		if err != nil || !bytes.Equal(state.staged, staged) {
			return errors.New("source index changed while copying; retry")
		}
	}
	// Only the indices of unexposed AO worktrees change before the directory swap.
	committed := false
	defer func() {
		if !committed {
			for _, state := range states {
				_ = exec.Command("git", "-C", filepath.Join(destination, state.rel), "reset", "--mixed", "HEAD").Run()
			}
		}
	}()
	for _, state := range states {
		if len(state.staged) == 0 {
			continue
		}
		cmd := exec.CommandContext(ctx, "git", "-C", filepath.Join(destination, state.rel), "apply", "--cached", "--binary", "-")
		cmd.Stdin = bytes.NewReader(state.staged)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("preserve staged edits: %w: %s", err, out)
		}
	}
	if completionMarker != "" {
		name := filepath.Base(completionMarker)
		if _, err := os.Lstat(filepath.Join(temp, name)); !errors.Is(err, os.ErrNotExist) {
			return errors.New("source contains a reserved workspace transfer marker")
		}
		id := strings.TrimPrefix(name, ".ao-import-complete-")
		if err := os.WriteFile(filepath.Join(temp, name), []byte(id), 0o600); err != nil {
			return err
		}
	}
	var moved []string
	restoreGit := func() {
		for _, rel := range moved {
			_ = os.Rename(filepath.Join(temp, rel, ".git"), filepath.Join(destination, rel, ".git"))
		}
	}
	for _, rel := range gitPaths {
		if err := os.MkdirAll(filepath.Join(temp, rel), 0o700); err != nil {
			restoreGit()
			return err
		}
		if err := os.Rename(filepath.Join(destination, rel, ".git"), filepath.Join(temp, rel, ".git")); err != nil {
			restoreGit()
			return err
		}
		moved = append(moved, rel)
	}
	backup := temp + "-empty"
	if err := os.Rename(destination, backup); err != nil {
		restoreGit()
		return err
	}
	if err := os.Rename(temp, destination); err != nil {
		_ = os.Rename(backup, destination)
		restoreGit()
		return err
	}
	committed = true
	_ = os.RemoveAll(backup)
	return nil
}

func providerRefusedImportOwnership(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, refusal := range []string{"this is open in another app", "session is already in use", "thread is already in use", "session is locked by another process"} {
		if strings.Contains(message, refusal) {
			return true
		}
	}
	return false
}
