package workspacewatch

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

var maxWatchedDirectories = 4096

type dirWatchLimiter struct {
	watcher   *fsnotify.Watcher
	limit     int
	mu        sync.Mutex
	watched   map[string]struct{}
	used      int
	skipped   int
	lastError error
}

func newDirWatchLimiter(watcher *fsnotify.Watcher, limit int) *dirWatchLimiter {
	return &dirWatchLimiter{watcher: watcher, limit: limit, watched: make(map[string]struct{})}
}

func (l *dirWatchLimiter) add(ctx context.Context, dir string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.watched[dir]; ok {
		return nil
	}
	if l.used >= l.limit {
		l.skipped++
		return nil
	}
	if err := l.watcher.Add(dir); err != nil {
		l.skipped++
		l.lastError = err
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	}
	l.watched[dir] = struct{}{}
	l.used++
	return nil
}

func (l *dirWatchLimiter) release(dir string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.watched[dir]; !ok {
		return
	}
	delete(l.watched, dir)
	_ = l.watcher.Remove(dir)
	l.used--
}

func (l *dirWatchLimiter) warn(ctx context.Context, stage string) {
	l.mu.Lock()
	skipped, lastError, limit := l.skipped, l.lastError, l.limit
	l.mu.Unlock()
	if skipped == 0 {
		return
	}
	slog.WarnContext(ctx, "workspace watch skipped directories", "stage", stage, "skipped", skipped, "watch_limit", limit, "last_error", lastError)
}

func Watch(ctx context.Context, roots ...string) (<-chan struct{}, error) {
	if len(roots) == 0 {
		return nil, fmt.Errorf("at least one workspace path is required")
	}

	unique := make(map[string]struct{}, len(roots))
	uniqueRoots := make([]string, 0, len(roots))
	for _, root := range roots {
		cleanRoot, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("resolve workspace path: %w", err)
		}
		if _, seen := unique[cleanRoot]; seen {
			continue
		}
		unique[cleanRoot] = struct{}{}
		uniqueRoots = append(uniqueRoots, cleanRoot)
	}
	if len(uniqueRoots) == 1 {
		return watchRoot(ctx, uniqueRoots[0], nil)
	}

	watchCtx, cancel := context.WithCancel(ctx)
	sources := make([]<-chan struct{}, 0, len(uniqueRoots))
	for _, cleanRoot := range uniqueRoots {
		source, err := watchRoot(watchCtx, cleanRoot, nil)
		if err != nil {
			cancel()
			return nil, err
		}
		sources = append(sources, source)
	}
	changes := make(chan struct{}, 1)
	var group sync.WaitGroup
	group.Add(len(sources))
	for _, source := range sources {
		go func() {
			defer group.Done()
			for range source {
				select {
				case changes <- struct{}{}:
				default:
				}
			}
		}()
	}
	go func() {
		group.Wait()
		cancel()
		close(changes)
	}()
	return changes, nil
}

func WatchSkipping(ctx context.Context, root string, skip func(rel string) bool) (<-chan struct{}, error) {
	return watchRoot(ctx, root, skip)
}

func watchRoot(ctx context.Context, root string, skip func(rel string) bool) (<-chan struct{}, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace path: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat workspace path: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace path is not a directory")
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create workspace watcher: %w", err)
	}

	git := discoverGitWorkspace(ctx, root)
	git.skip = skip
	limiter := newDirWatchLimiter(watcher, maxWatchedDirectories)
	if err := addInitialDirectories(ctx, limiter, root, git); err != nil {
		_ = watcher.Close()
		return nil, err
	}
	limiter.warn(ctx, "initial")

	changes := make(chan struct{}, 1)
	go run(ctx, watcher, root, git, limiter, changes)
	return changes, nil
}

type gitWorkspace struct {
	available     bool
	files         []string
	metadataFiles map[string]struct{}
	skip          func(rel string) bool
}

func (g gitWorkspace) skips(root, target string) bool {
	if g.skip == nil {
		return false
	}
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != "." && g.skip(filepath.ToSlash(rel))
}

func discoverGitWorkspace(ctx context.Context, root string) gitWorkspace {
	topRaw, err := aoprocess.CommandContext(ctx, "git", "-C", root, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return gitWorkspace{}
	}
	toplevel := filepath.Clean(filepath.FromSlash(strings.TrimSpace(string(topRaw))))
	samePath := toplevel == root || (runtime.GOOS == "windows" && strings.EqualFold(toplevel, root))
	if !samePath {
		topInfo, topErr := os.Stat(toplevel)
		rootInfo, rootErr := os.Stat(root)
		if topErr != nil || rootErr != nil || !os.SameFile(topInfo, rootInfo) {
			return gitWorkspace{}
		}
	}
	gitDirRaw, err := aoprocess.CommandContext(ctx, "git", "-C", root, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		return gitWorkspace{}
	}
	filesRaw, err := aoprocess.CommandContext(ctx, "git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		return gitWorkspace{}
	}
	files := make([]string, 0)
	for _, raw := range bytes.Split(filesRaw, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		files = append(files, filepath.FromSlash(string(raw)))
	}
	gitDir := strings.TrimSpace(string(gitDirRaw))
	metadataFiles := map[string]struct{}{
		filepath.Join(gitDir, "HEAD"): {},
	}
	commonDir := gitDir
	if raw, commonErr := aoprocess.CommandContext(ctx, "git", "-C", root, "rev-parse", "--git-common-dir").Output(); commonErr == nil {
		commonDir = strings.TrimSpace(string(raw))
		if !filepath.IsAbs(commonDir) {
			commonDir = filepath.Join(root, commonDir)
		}
	}
	metadataFiles[filepath.Join(commonDir, "packed-refs")] = struct{}{}
	metadataFiles[filepath.Join(commonDir, "config")] = struct{}{}
	if raw, refErr := aoprocess.CommandContext(ctx, "git", "-C", root, "symbolic-ref", "-q", "HEAD").Output(); refErr == nil {
		ref := strings.TrimSpace(string(raw))
		if ref != "" {
			metadataFiles[filepath.Join(commonDir, filepath.FromSlash(ref))] = struct{}{}
			if branch, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
				metadataFiles[filepath.Join(commonDir, "refs", "remotes", "origin", filepath.FromSlash(branch))] = struct{}{}
			}
		}
	}
	return gitWorkspace{available: true, files: files, metadataFiles: metadataFiles}
}

func addInitialDirectories(ctx context.Context, limiter *dirWatchLimiter, root string, git gitWorkspace) error {
	dirs := map[string]struct{}{root: {}}
	if git.available {
		for _, rel := range git.files {
			dir := filepath.Dir(filepath.Join(root, rel))
			for isWithin(root, dir) {
				dirs[dir] = struct{}{}
				if dir == root {
					break
				}
				dir = filepath.Dir(dir)
			}
		}
		for metadataFile := range git.metadataFiles {
			if info, err := os.Stat(filepath.Dir(metadataFile)); err == nil && info.IsDir() {
				dirs[filepath.Dir(metadataFile)] = struct{}{}
			}
		}
	} else if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		if path != root && (entry.Name() == ".git" || git.skips(root, path)) {
			return filepath.SkipDir
		}
		dirs[path] = struct{}{}
		return nil
	}); err != nil {
		return fmt.Errorf("walk workspace directories: %w", err)
	}
	for dir := range dirs {
		if git.skips(root, dir) {
			delete(dirs, dir)
		}
	}

	ordered := make([]string, 0, len(dirs))
	for dir := range dirs {
		ordered = append(ordered, dir)
	}
	sort.Slice(ordered, func(i, j int) bool {
		iDepth := directoryDepth(root, ordered[i])
		jDepth := directoryDepth(root, ordered[j])
		if iDepth != jDepth {
			return iDepth < jDepth
		}
		return ordered[i] < ordered[j]
	})
	if len(ordered) > maxWatchedDirectories {
		ordered = ordered[:maxWatchedDirectories]
	}

	for _, dir := range ordered {
		if err := limiter.add(ctx, dir); err != nil {
			return err
		}
	}
	return nil
}

func directoryDepth(root, dir string) int {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." {
		return 0
	}
	return strings.Count(rel, string(filepath.Separator)) + 1
}

func run(ctx context.Context, watcher *fsnotify.Watcher, root string, git gitWorkspace, limiter *dirWatchLimiter, changes chan struct{}) {
	defer close(changes)
	defer func() { _ = watcher.Close() }()
	defer limiter.warn(ctx, "runtime")
	notify := func() {
		select {
		case changes <- struct{}{}:
		default:
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-watcher.Errors:
			if !ok {
				return
			}
			notify()
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if handleEvent(ctx, limiter, root, git, event) {
				notify()
			}
		}
	}
}

func handleEvent(ctx context.Context, limiter *dirWatchLimiter, root string, git gitWorkspace, event fsnotify.Event) bool {
	if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
		return false
	}
	name, err := filepath.Abs(event.Name)
	if err != nil {
		return false
	}
	if _, ok := git.metadataFiles[name]; ok {
		return true
	}
	if !isWithin(root, name) || hasGitMetadataComponent(root, name) || git.skips(root, name) {
		return false
	}
	if git.available && gitIgnored(ctx, root, name) {
		return false
	}
	if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
		limiter.release(name)
	}
	if event.Has(fsnotify.Create) {
		if info, statErr := os.Stat(name); statErr == nil && info.IsDir() {
			_ = addCreatedTree(ctx, limiter, root, git, name)
		}
	}
	return true
}

func addCreatedTree(ctx context.Context, limiter *dirWatchLimiter, root string, git gitWorkspace, created string) error {
	return filepath.WalkDir(created, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		if hasGitMetadataComponent(root, path) || git.skips(root, path) || (git.available && gitIgnored(ctx, root, path)) {
			return filepath.SkipDir
		}
		return limiter.add(ctx, path)
	})
}

func gitIgnored(ctx context.Context, root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return aoprocess.CommandContext(ctx, "git", "-C", root, "check-ignore", "-q", "--", filepath.ToSlash(rel)).Run() == nil
}

func isWithin(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func hasGitMetadataComponent(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return true
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == ".git" {
			return true
		}
	}
	return false
}
