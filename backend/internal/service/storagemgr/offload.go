package storagemgr

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// offloadableNames are folder names whose contents a package manager or build
// recreates. A folder is offloaded only when its name is here (or a project
// lists its path) AND git reports it ignored: tracked or untracked-but-not-
// ignored files are never touched.
var offloadableNames = nameSet(
	"node_modules", "dist", "build", "out", ".next", ".nuxt", ".svelte-kit", ".turbo", ".vite",
	".parcel-cache", ".angular", ".expo", ".cache", "coverage", "target", ".gradle", "__pycache__",
	".pytest_cache", ".mypy_cache", ".ruff_cache", ".tox", ".venv", ".dart_tool", "Pods", "DerivedData",
	".terraform", ".webpack", "storybook-static",
)

func nameSet(names ...string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[name] = true
	}
	return set
}

// protectedSegments are path segments an offload never removes, even when a
// project lists them and git ignores them: secrets, AO's own files and agent
// or editor state.
var protectedSegments = []string{".git", ".ao", ".claude", ".codex", ".agents", ".vscode", ".idea", ".ssh", ".aws"}

func isProtected(rel string) bool {
	for _, segment := range strings.Split(rel, "/") {
		lower := strings.ToLower(segment)
		if strings.HasPrefix(lower, ".env") || strings.HasSuffix(lower, ".pem") || strings.HasSuffix(lower, ".key") {
			return true
		}
		for _, protected := range protectedSegments {
			if lower == protected {
				return true
			}
		}
	}
	return false
}

// offloadTarget is one folder an offload would remove.
type offloadTarget struct {
	Rel   string // slash-separated, relative to the worktree
	Bytes int64
}

// gitCommand runs non-interactive, hook-free git in dir.
func gitCommand(ctx context.Context, dir string, args ...string) ([]byte, error) {
	global := []string{"--no-pager", "--no-optional-locks", "-c", "core.hooksPath=" + os.DevNull, "-c", "core.fsmonitor=false", "-C", dir}
	cmd := aoprocess.CommandContext(ctx, "git", append(global, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// findOffloadTargets lists the folders an offload of worktree would remove.
func findOffloadTargets(ctx context.Context, worktree string, projectPaths []string) ([]offloadTarget, error) {
	out, err := gitCommand(ctx, worktree, "status", "--porcelain=v1", "-z", "--ignored=traditional", "--untracked-files=normal")
	if err != nil {
		return nil, err
	}
	selected := map[string]bool{}
	for _, record := range bytes.Split(out, []byte{0}) {
		line := string(record)
		if !strings.HasPrefix(line, "!! ") {
			continue
		}
		rel := strings.TrimPrefix(line, "!! ")
		if !strings.HasSuffix(rel, "/") {
			continue // a single ignored file; only folders are offloaded
		}
		rel = strings.TrimSuffix(rel, "/")
		if offloadableNames[path.Base(rel)] && !isProtected(rel) {
			selected[rel] = true
		}
	}
	for _, raw := range projectPaths {
		rel := strings.Trim(filepath.ToSlash(filepath.Clean(strings.TrimSpace(raw))), "/")
		if rel == "" || rel == "." || strings.HasPrefix(rel, "../") || rel == ".." || isProtected(rel) {
			continue
		}
		info, statErr := os.Lstat(filepath.Join(worktree, filepath.FromSlash(rel)))
		if statErr != nil || !info.IsDir() {
			continue
		}
		// check-ignore exits 0 only when git ignores the path.
		if _, ignoreErr := gitCommand(ctx, worktree, "check-ignore", "-q", "--", rel); ignoreErr == nil {
			selected[rel] = true
		}
	}
	rels := make([]string, 0, len(selected))
	for rel := range selected {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	targets := make([]offloadTarget, 0, len(rels))
	for _, rel := range rels {
		// Skip a folder nested in one already selected: removing the parent
		// removes it.
		nested := false
		for _, other := range rels {
			if other != rel && strings.HasPrefix(rel, other+"/") {
				nested = true
				break
			}
		}
		if nested {
			continue
		}
		size, sizeErr := treeSize(ctx, filepath.Join(worktree, filepath.FromSlash(rel)))
		if sizeErr != nil {
			return nil, sizeErr
		}
		if size > 0 {
			targets = append(targets, offloadTarget{Rel: rel, Bytes: size})
		}
	}
	return targets, nil
}

// lockfileNames are the package-manager lockfiles a dependency template is
// keyed on, in lookup order.
var lockfileNames = []string{"package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml", "yarn.lock", "bun.lock", "bun.lockb"}

// lockfileHash fingerprints the dependency set a node_modules in dir holds:
// the lockfile, the OS and CPU (native modules), and the Node major version.
// Empty when dir has no lockfile, so nothing is templated by guesswork.
func lockfileHash(dir, nodeVersion string) string {
	for _, name := range lockfileNames {
		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		sum := sha256.New()
		_, _ = fmt.Fprintf(sum, "%s\x00%s\x00%s\x00%s\x00", name, runtime.GOOS, runtime.GOARCH, nodeVersion)
		_, _ = sum.Write(content)
		return hex.EncodeToString(sum.Sum(nil))
	}
	return ""
}

// templateKey names the template for one dependency folder of one project.
func templateKey(projectID domain.ProjectID, rel, lockHash string) string {
	sum := sha256.Sum256([]byte(string(projectID) + "\x00" + rel + "\x00" + lockHash))
	return hex.EncodeToString(sum[:])[:32]
}

// templateMeta is stored beside a template's tree.
type templateMeta struct {
	ProjectID domain.ProjectID `json:"projectId"`
	Rel       string           `json:"rel"`
	LockHash  string           `json:"lockHash"`
	CreatedAt time.Time        `json:"createdAt"`
}

const (
	templateTreeName = "tree"
	templateMetaName = "template.json"
	templateUsedName = "last-used"
)

func (s *Service) templateDir(key string) string { return filepath.Join(s.layout.Templates(), key) }

func (s *Service) touchTemplate(key string) {
	marker := filepath.Join(s.templateDir(key), templateUsedName)
	now := s.now()
	if err := os.Chtimes(marker, now, now); err != nil {
		_ = os.WriteFile(marker, nil, 0o600)
	}
}

// adoptTemplate moves a session's dependency folder into a new template with
// one rename. It reports false when a template for the key already exists or
// the move cannot happen, leaving the folder for the trash.
func (s *Service) adoptTemplate(key string, meta templateMeta, abs string) bool {
	dir := s.templateDir(key)
	tree := filepath.Join(dir, templateTreeName)
	if pathExists(tree) {
		return false
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return false
	}
	encoded, err := json.Marshal(meta)
	if err != nil {
		return false
	}
	if err := os.WriteFile(filepath.Join(dir, templateMetaName), encoded, 0o600); err != nil {
		return false
	}
	if err := os.Rename(abs, tree); err != nil {
		_ = os.RemoveAll(dir)
		return false
	}
	s.touchTemplate(key)
	return true
}

// offloadWorktree removes a worktree's dependency and build folders, moving
// node_modules into a template when the filesystem can clone it back. The
// caller holds the session's quiet guard.
func (s *Service) offloadWorktree(ctx context.Context, project domain.ProjectRecord, worktree string) (domain.SessionStorageFacts, error) {
	facts := domain.SessionStorageFacts{TemplateKeys: map[string]string{}}
	if !Within(s.layout.Worktrees(), worktree) {
		return facts, errors.New("worktree is outside AO's managed worktrees")
	}
	targets, err := findOffloadTargets(ctx, worktree, project.Config.StorageOffloadPaths)
	if err != nil {
		return facts, err
	}
	cloneable := s.cloneSupported()
	nodeVersion := ""
	if cloneable {
		nodeVersion = s.nodeVersion(ctx)
	}
	var errs []error
	for _, target := range targets {
		abs := filepath.Join(worktree, filepath.FromSlash(target.Rel))
		if !Within(worktree, abs) {
			continue
		}
		key := ""
		if cloneable && path.Base(target.Rel) == "node_modules" {
			if lockHash := lockfileHash(filepath.Dir(abs), nodeVersion); lockHash != "" {
				key = templateKey(domain.ProjectID(project.ID), target.Rel, lockHash)
				meta := templateMeta{ProjectID: domain.ProjectID(project.ID), Rel: target.Rel, LockHash: lockHash, CreatedAt: s.now()}
				if s.adoptTemplate(key, meta, abs) {
					facts.OffloadedPaths = append(facts.OffloadedPaths, target.Rel)
					facts.OffloadedBytes += target.Bytes
					facts.TemplateKeys[target.Rel] = key
					continue
				}
				if !pathExists(filepath.Join(s.templateDir(key), templateTreeName)) {
					key = ""
				}
			}
		}
		if _, err := moveToTrash(s.layout.Trash(), abs); err != nil {
			errs = append(errs, err)
			continue
		}
		facts.OffloadedPaths = append(facts.OffloadedPaths, target.Rel)
		facts.OffloadedBytes += target.Bytes
		if key != "" {
			facts.TemplateKeys[target.Rel] = key
		}
	}
	return facts, errors.Join(errs...)
}

// restoreFromTemplate clones a template back into a worktree when the
// worktree's lockfile still matches the template's.
func (s *Service) restoreFromTemplate(ctx context.Context, project domain.ProjectRecord, worktree, rel, key string) error {
	if !s.cloneSupported() {
		return errCloneUnsupported
	}
	abs := filepath.Join(worktree, filepath.FromSlash(rel))
	if lockHash := lockfileHash(filepath.Dir(abs), s.nodeVersion(ctx)); lockHash == "" || templateKey(domain.ProjectID(project.ID), rel, lockHash) != key {
		return errors.New("lockfile changed since offload")
	}
	tree := filepath.Join(s.templateDir(key), templateTreeName)
	if !pathExists(tree) {
		return errors.New("template is gone")
	}
	if err := cloneTreeAtomic(tree, abs); err != nil {
		return err
	}
	s.touchTemplate(key)
	return nil
}

// RestoreDependencies implements sessionmanager.StorageHooks.
func (s *Service) RestoreDependencies(ctx context.Context, project domain.ProjectRecord, rec domain.SessionRecord) (bool, func(error), error) {
	facts, ok, err := s.store.GetSessionStorageFacts(ctx, rec.ID)
	if err != nil || !ok || !facts.Offloaded() {
		return false, nil, err
	}
	worktree := rec.Metadata.WorkspacePath
	var missing []string
	for _, rel := range facts.OffloadedPaths {
		if !pathExists(filepath.Join(worktree, filepath.FromSlash(rel))) {
			missing = append(missing, rel)
		}
	}
	cloned := 0
	for _, rel := range missing {
		key := facts.TemplateKeys[rel]
		if key == "" {
			continue
		}
		if cloneErr := s.restoreFromTemplate(ctx, project, worktree, rel, key); cloneErr == nil {
			cloned++
		} else {
			s.logger.Debug("storage: template restore skipped", "sessionID", rec.ID, "path", rel, "error", cloneErr)
		}
	}
	needsSetup := cloned < len(missing)
	method := "copy-on-write clone"
	if needsSetup {
		method = "project Setup"
	}
	if len(missing) == 0 {
		method = "already present"
	}
	finish := func(setupErr error) {
		now := s.now()
		action := domain.StorageAction{
			At: now, Kind: domain.StorageCleanRestore, Target: worktree, SessionID: rec.ID,
			Reason: "dependencies restored by " + method, Result: domain.StorageActionDone,
		}
		errText := ""
		if setupErr != nil {
			errText = setupErr.Error()
			action.Result, action.Error = domain.StorageActionFailed, errText
		}
		if recordErr := s.store.RecordSessionRestore(ctx, rec.ID, now, errText); recordErr != nil {
			s.logger.Warn("storage: record restore", "sessionID", rec.ID, "error", recordErr)
		}
		s.audit(ctx, action)
	}
	if needsSetup && len(project.Config.PostCreate) == 0 {
		// Nothing can reinstall them for the agent; say so and keep the
		// session usable.
		return false, func(error) {
			finish(errors.New("no Setup script is configured to reinstall offloaded dependencies; run the project's install command"))
		}, nil
	}
	return needsSetup, finish, nil
}

// SeedWorkspace implements sessionmanager.StorageHooks: before Setup runs in a
// new or recreated worktree, it clones every template whose lockfile matches,
// so an incremental install has almost nothing left to do.
func (s *Service) SeedWorkspace(ctx context.Context, project domain.ProjectRecord, workspacePath string) {
	if !s.cloneSupported() {
		return
	}
	entries, err := os.ReadDir(s.layout.Templates())
	if err != nil {
		return
	}
	nodeVersion := s.nodeVersion(ctx)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		meta, ok := s.readTemplateMeta(entry.Name())
		if !ok || meta.ProjectID != domain.ProjectID(project.ID) {
			continue
		}
		abs := filepath.Join(workspacePath, filepath.FromSlash(meta.Rel))
		if pathExists(abs) || lockfileHash(filepath.Dir(abs), nodeVersion) != meta.LockHash {
			continue
		}
		if err := cloneTreeAtomic(filepath.Join(s.templateDir(entry.Name()), templateTreeName), abs); err != nil {
			s.logger.Debug("storage: seed from template skipped", "path", meta.Rel, "error", err)
			continue
		}
		s.touchTemplate(entry.Name())
	}
}

func (s *Service) readTemplateMeta(key string) (templateMeta, bool) {
	content, err := os.ReadFile(filepath.Join(s.templateDir(key), templateMetaName))
	if err != nil {
		return templateMeta{}, false
	}
	var meta templateMeta
	if err := json.Unmarshal(content, &meta); err != nil || meta.Rel == "" {
		return templateMeta{}, false
	}
	return meta, true
}

// nodeVersion is the Node.js major version on PATH, cached for a few minutes;
// native modules built for one major do not load in another.
func (s *Service) nodeVersion(ctx context.Context) string {
	s.nodeMu.Lock()
	defer s.nodeMu.Unlock()
	if !s.nodeCheckedAt.IsZero() && s.now().Sub(s.nodeCheckedAt) < 10*time.Minute {
		return s.nodeVersionValue
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := aoprocess.CommandContext(probeCtx, "node", "--version").Output()
	version := ""
	if err == nil {
		version = strings.TrimSpace(string(out))
		if major, _, found := strings.Cut(version, "."); found {
			version = major
		}
	}
	s.nodeVersionValue, s.nodeCheckedAt = version, s.now()
	return version
}

// cloneSupported reports, once per service, whether the data dir's filesystem
// clones copy-on-write.
func (s *Service) cloneSupported() bool {
	s.cloneOnce.Do(func() {
		s.cloneOK = probeClone(s.layout.Templates())
	})
	return s.cloneOK
}
