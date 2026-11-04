package usage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

const (
	transcriptEventBuffer = 256
	watcherErrorBuffer    = 16
)

// TranscriptEvent reports a transcript path whose contents or filesystem
// identity may have changed.
type TranscriptEvent struct {
	Path      string
	Discovery bool
}

// TranscriptWatcher watches exact registered transcript files. Exact-file
// watches keep descriptor use proportional to active durable sources even on
// kqueue, where watching a large directory can open every file inside it.
type TranscriptWatcher struct {
	mu          sync.Mutex
	watcher     *fsnotify.Watcher
	roots       []string
	sourcePaths []string
	watched     map[string]struct{}
	closed      bool

	events chan TranscriptEvent
	errors chan error
	done   chan struct{}

	startOnce sync.Once
}

// NewTranscriptWatcher creates an empty watcher. The coordinator supplies the
// current durable source inventory through Rebuild before ingestion begins.
func NewTranscriptWatcher(ctx context.Context, roots []string) (*TranscriptWatcher, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	normalized, err := resolveWatchRoots(ctx, roots)
	if err != nil {
		return nil, err
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create transcript watcher: %w", err)
	}
	result := &TranscriptWatcher{
		watcher: watcher,
		roots:   normalized,
		watched: make(map[string]struct{}),
		events:  make(chan TranscriptEvent, transcriptEventBuffer),
		errors:  make(chan error, watcherErrorBuffer),
		done:    make(chan struct{}),
	}
	return result, nil
}

func resolveWatchRoots(ctx context.Context, roots []string) ([]string, error) {
	normalized, err := normalizeTranscriptRoots(roots)
	if err != nil {
		return nil, err
	}
	for index, root := range normalized {
		resolved, err := resolveTranscriptRoot(ctx, root)
		if err != nil {
			return nil, fmt.Errorf("resolve transcript root: %w", redactFilesystemError(err))
		}
		normalized[index] = resolved
		info, err := os.Stat(resolved)
		if err == nil && !info.IsDir() {
			return nil, errors.New("transcript root is not a directory")
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("inspect transcript root: %w", redactFilesystemError(err))
		}
	}
	return normalized, nil
}

// SetRoots refreshes the allowed provider directories before Rebuild updates
// exact-file watches. The coordinator calls it when session inventory changes.
func (w *TranscriptWatcher) SetRoots(ctx context.Context, roots []string) error {
	resolved, err := resolveWatchRoots(ctx, roots)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return errors.New("transcript watcher is closed")
	}
	w.roots = resolved
	return nil
}

// Events returns filesystem-triggered transcript changes.
func (w *TranscriptWatcher) Events() <-chan TranscriptEvent {
	return w.events
}

// Errors returns watcher errors, including fsnotify queue-overflow errors and
// failures encountered while rebuilding the exact-file watch set.
func (w *TranscriptWatcher) Errors() <-chan error {
	return w.errors
}

// Start begins forwarding watcher events until ctx is cancelled. Start is
// idempotent; subsequent calls return the completion channel for the original
// run.
func (w *TranscriptWatcher) Start(ctx context.Context) <-chan struct{} {
	w.startOnce.Do(func() {
		go w.run(ctx)
	})
	return w.done
}

// Rebuild replaces the current watch set using exact durable source paths. It
// is safe during event handling and also serves as fsnotify-overflow recovery.
func (w *TranscriptWatcher) Rebuild(ctx context.Context, sourcePaths []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if w.closed {
		return errors.New("transcript watcher is closed")
	}
	w.sourcePaths = w.sourcePaths[:0]
	for _, path := range sourcePaths {
		path = canonicalTranscriptPath(path)
		if w.withinDesiredRoot(ctx, path) {
			w.sourcePaths = append(w.sourcePaths, path)
		}
	}
	sort.Strings(w.sourcePaths)
	return w.rebuildLocked(ctx)
}

func (w *TranscriptWatcher) run(ctx context.Context) {
	defer close(w.done)
	defer close(w.events)
	defer close(w.errors)

	for {
		select {
		case <-ctx.Done():
			w.close()
			return
		case event, ok := <-w.watcher.Events:
			if !ok {
				w.close()
				return
			}
			emit, discovery, rebuildErr := w.handleEvent(ctx, event)
			if rebuildErr != nil && !w.sendError(ctx, rebuildErr) {
				w.close()
				return
			}
			if emit != "" && !w.sendEvent(ctx, TranscriptEvent{Path: emit, Discovery: discovery}) {
				w.close()
				return
			}
		case err, ok := <-w.watcher.Errors:
			if !ok {
				w.close()
				return
			}
			if err != nil && !w.sendError(ctx, err) {
				w.close()
				return
			}
		}
	}
}

func (w *TranscriptWatcher) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.closed = true
	_ = w.watcher.Close()
	clear(w.watched)
}

func (w *TranscriptWatcher) handleEvent(ctx context.Context, event fsnotify.Event) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	path := canonicalTranscriptPath(event.Name)
	emit := ""
	discovery := false
	w.mu.Lock()
	withinRoot := w.withinDesiredRoot(ctx, path)
	w.mu.Unlock()
	if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename|fsnotify.Remove) != 0 &&
		filepath.Ext(path) == ".jsonl" &&
		withinRoot {
		emit = path
		discovery = event.Op&(fsnotify.Create|fsnotify.Rename) != 0
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || event.Op&(fsnotify.Rename|fsnotify.Remove) == 0 {
		return emit, discovery, nil
	}
	if _, watched := w.watched[path]; !watched {
		return emit, discovery, nil
	}
	_ = w.watcher.Remove(path)
	delete(w.watched, path)
	if err := w.rebuildLocked(ctx); err != nil {
		return emit, discovery, fmt.Errorf("rebuild transcript watcher after %s: %w", event.Op, err)
	}
	return emit, discovery, nil
}

func (w *TranscriptWatcher) rebuildLocked(ctx context.Context) error {
	desired, err := w.desiredWatchSetLocked(ctx)
	if err != nil {
		return err
	}
	for path := range w.watched {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, keep := desired[path]; keep {
			continue
		}
		if err := w.watcher.Remove(path); err != nil &&
			!errors.Is(err, fsnotify.ErrNonExistentWatch) {
			return fmt.Errorf("remove stale transcript watch: %w", redactFilesystemError(err))
		}
		delete(w.watched, path)
	}

	paths := make([]string, 0, len(desired))
	for path := range desired {
		if _, watched := w.watched[path]; !watched {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := w.watcher.Add(path); err != nil {
			return fmt.Errorf("watch transcript source: %w", redactFilesystemError(err))
		}
		w.watched[path] = struct{}{}
	}
	return nil
}

func (w *TranscriptWatcher) desiredWatchSetLocked(ctx context.Context) (map[string]struct{}, error) {
	result := make(map[string]struct{})
	for _, path := range w.sourcePaths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := os.Stat(path)
		switch {
		case err == nil && info.Mode().IsRegular():
			result[path] = struct{}{}
		case err == nil:
			continue
		case errors.Is(err, os.ErrNotExist):
			continue
		default:
			return nil, fmt.Errorf("inspect transcript source: %w", redactFilesystemError(err))
		}
	}
	return result, nil
}

func (w *TranscriptWatcher) withinDesiredRoot(ctx context.Context, path string) bool {
	for _, root := range w.roots {
		if ctx.Err() != nil {
			return false
		}
		if pathWithin(path, root) {
			return true
		}
		resolved, err := resolveTranscriptRoot(ctx, root)
		if err == nil && pathWithin(path, resolved) {
			return true
		}
	}
	return false
}

func (w *TranscriptWatcher) sendEvent(ctx context.Context, event TranscriptEvent) bool {
	select {
	case w.events <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

func (w *TranscriptWatcher) sendError(ctx context.Context, err error) bool {
	select {
	case w.errors <- err:
		return true
	case <-ctx.Done():
		return false
	}
}

func normalizeTranscriptRoots(roots []string) ([]string, error) {
	seen := make(map[string]struct{}, len(roots))
	result := make([]string, 0, len(roots))
	for _, root := range roots {
		if strings.TrimSpace(root) == "" {
			return nil, errors.New("transcript root cannot be empty")
		}
		absolute, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("normalize transcript root: %w", redactFilesystemError(err))
		}
		absolute = filepath.Clean(absolute)
		if _, ok := seen[absolute]; ok {
			continue
		}
		seen[absolute] = struct{}{}
		result = append(result, absolute)
	}
	sort.Strings(result)
	return result, nil
}

func resolveTranscriptRoot(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	switch {
	case err == nil:
		return filepath.Clean(resolved), nil
	case errors.Is(err, os.ErrNotExist):
		return filepath.Clean(path), nil
	default:
		return "", err
	}
}

func redactFilesystemError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, os.ErrNotExist):
		return os.ErrNotExist
	case errors.Is(err, os.ErrPermission):
		return os.ErrPermission
	default:
		return errors.New("filesystem operation failed")
	}
}

func pathWithin(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative == "." ||
		(relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}
