package usage

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	defaultWorkerCount            = 2
	defaultWorkQueueSize          = 256
	defaultCoordinatorRetry       = 30 * time.Second
	refreshRetryID          int64 = 0
)

type coordinatorStore interface {
	ListWatchableUsageSources(context.Context) ([]domain.UsageSourceRecord, error)
	ListFinalizingUsageSourceIDs(context.Context) ([]int64, error)
	HasPendingUsageDiscovery(context.Context) (bool, error)
}

type sourceIngestor interface {
	Ingest(context.Context, int64) (IngestResult, error)
}

type transcriptWatcher interface {
	Events() <-chan TranscriptEvent
	Errors() <-chan error
	Start(context.Context) <-chan struct{}
	SetRoots(context.Context, []string) error
	Rebuild(context.Context, []string) error
}

// CoordinatorConfig configures the event-driven usage pipeline.
type CoordinatorConfig struct {
	Workers       int
	QueueSize     int
	Clock         func() time.Time
	RetryDelay    time.Duration
	Logger        *slog.Logger
	Initialize    func(context.Context) error
	Reconcile     func(context.Context) error
	ReconcilePath func(context.Context, string) error
	WatchRoots    func(context.Context) ([]string, error)
}

// Coordinator ingests unread usage sources when Flush asks, and finalizing sources at once.
type Coordinator struct {
	store         coordinatorStore
	ingestor      sourceIngestor
	watcher       transcriptWatcher
	workers       int
	queueSize     int
	now           func() time.Time
	retryDelay    time.Duration
	logger        *slog.Logger
	initialize    func(context.Context) error
	reconcile     func(context.Context) error
	reconcilePath func(context.Context, string) error
	watchRoots    func(context.Context) ([]string, error)
	refresh       chan struct{}
	inventory     chan struct{}
	flushes       chan chan struct{}
	stopped       chan struct{}
}

// NewCoordinator constructs an event-driven usage coordinator.
func NewCoordinator(
	store coordinatorStore,
	ingestor sourceIngestor,
	watcher transcriptWatcher,
	cfg CoordinatorConfig,
) *Coordinator {
	if cfg.Workers <= 0 {
		cfg.Workers = defaultWorkerCount
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultWorkQueueSize
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.RetryDelay <= 0 {
		cfg.RetryDelay = defaultCoordinatorRetry
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Coordinator{
		store:         store,
		ingestor:      ingestor,
		watcher:       watcher,
		workers:       cfg.Workers,
		queueSize:     cfg.QueueSize,
		now:           cfg.Clock,
		retryDelay:    cfg.RetryDelay,
		logger:        cfg.Logger,
		initialize:    cfg.Initialize,
		reconcile:     cfg.Reconcile,
		reconcilePath: cfg.ReconcilePath,
		watchRoots:    cfg.WatchRoots,
		refresh:       make(chan struct{}, 1),
		inventory:     make(chan struct{}, 1),
		flushes:       make(chan chan struct{}),
		stopped:       make(chan struct{}),
	}
}

// NotifySourcesChanged requests source discovery and inventory rebuilding
// without blocking an agent hook.
func (c *Coordinator) NotifySourcesChanged() {
	select {
	case c.refresh <- struct{}{}:
	default:
	}
}

// NotifyInventoryChanged requests a source inventory rebuild without running
// provider-specific discovery.
func (c *Coordinator) NotifyInventoryChanged() {
	select {
	case c.inventory <- struct{}{}:
	default:
	}
}

// Flush ingests every unread source and returns when none is queued or running, or ctx ends.
func (c *Coordinator) Flush(ctx context.Context) {
	done := make(chan struct{})
	select {
	case c.flushes <- done:
	case <-c.stopped:
		return
	case <-ctx.Done():
		return
	}
	select {
	case <-done:
	case <-c.stopped:
	case <-ctx.Done():
	}
}

type sourceWorkState struct {
	queued  bool
	running bool
	dirty   bool
}

type ingestionResult struct {
	sourceID int64
	result   IngestResult
	err      error
}

// Start runs until ctx is canceled and returns a channel closed after the
// watcher and ingestion workers have stopped.
func (c *Coordinator) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.run(ctx)
	}()
	return done
}

func (c *Coordinator) run(ctx context.Context) {
	defer close(c.stopped)
	watcherDone := c.watcher.Start(ctx)
	workerCtx, cancelWorkers := context.WithCancel(ctx)
	work := make(chan int64, c.queueSize)
	results := make(chan ingestionResult, c.workers)
	var workers sync.WaitGroup
	for range c.workers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for sourceID := range work {
				result, err := c.ingestor.Ingest(workerCtx, sourceID)
				select {
				case results <- ingestionResult{sourceID: sourceID, result: result, err: err}:
				case <-workerCtx.Done():
					return
				}
			}
		}()
	}
	defer func() {
		cancelWorkers()
		close(work)
		workers.Wait()
		<-watcherDone
	}()

	states := make(map[int64]*sourceWorkState)
	retries := make(map[int64]time.Time)
	paths := make(map[string][]int64)
	unread := make(map[int64]struct{})
	finalizing := make(map[int64]struct{})
	var waiters []chan struct{}
	var ready []int64
	running := 0
	var retryTimer *time.Timer
	initializePending := c.initialize != nil

	enqueue := func(sourceID int64) {
		delete(retries, sourceID)
		delete(unread, sourceID)
		state := states[sourceID]
		if state == nil {
			state = &sourceWorkState{}
			states[sourceID] = state
		}
		if state.running {
			state.dirty = true
			return
		}
		if state.queued {
			return
		}
		state.queued = true
		ready = append(ready, sourceID)
	}
	mark := func(sourceID int64) {
		if _, ok := finalizing[sourceID]; ok || len(waiters) > 0 {
			enqueue(sourceID)
			return
		}
		if state := states[sourceID]; state == nil || !state.queued {
			unread[sourceID] = struct{}{}
		}
	}
	scheduleRetry := func(sourceID int64, at time.Time) {
		if sourceID != refreshRetryID {
			if state := states[sourceID]; state != nil && (state.queued || state.running || state.dirty) {
				return
			}
		}
		retries[sourceID] = at.UTC()
	}

	refreshInventory := func(runDiscovery bool) {
		if initializePending {
			if err := c.initialize(ctx); err != nil {
				if ctx.Err() == nil {
					c.logger.Warn("usage source initialization failed", "err", err)
					scheduleRetry(refreshRetryID, c.now().UTC().Add(c.retryDelay))
				}
			} else {
				initializePending = false
			}
		}
		if runDiscovery && c.reconcile != nil {
			if err := c.reconcile(ctx); err != nil && ctx.Err() == nil {
				c.logger.Warn("usage source reconciliation failed", "err", err)
				scheduleRetry(refreshRetryID, c.now().UTC().Add(c.retryDelay))
			}
		}
		sources, err := c.store.ListWatchableUsageSources(ctx)
		var finalizingIDs []int64
		if err == nil {
			finalizingIDs, err = c.store.ListFinalizingUsageSourceIDs(ctx)
		}
		if err != nil {
			if ctx.Err() == nil {
				c.logger.Warn("load watchable usage sources failed", "err", err)
				scheduleRetry(refreshRetryID, c.now().UTC().Add(c.retryDelay))
			}
			return
		}
		clear(finalizing)
		for _, sourceID := range finalizingIDs {
			finalizing[sourceID] = struct{}{}
		}
		nextPaths := make(map[string][]int64)
		live := make(map[int64]struct{}, len(sources))
		now := c.now().UTC()
		for _, source := range sources {
			live[source.ID] = struct{}{}
			path := canonicalTranscriptPath(source.ArtifactPath)
			nextPaths[path] = append(nextPaths[path], source.ID)
			if source.NextRetryAt != nil && source.NextRetryAt.After(now) {
				scheduleRetry(source.ID, *source.NextRetryAt)
				continue
			}
			mark(source.ID)
		}
		sourcePaths := make([]string, 0, len(nextPaths))
		for path := range nextPaths {
			sourcePaths = append(sourcePaths, path)
		}
		if c.watchRoots != nil {
			roots, err := c.watchRoots(ctx)
			if err == nil {
				err = c.watcher.SetRoots(ctx, roots)
			}
			if err != nil {
				if ctx.Err() == nil {
					c.logger.Warn("resolve usage transcript watch roots failed", "err", err)
					scheduleRetry(refreshRetryID, c.now().UTC().Add(c.retryDelay))
				}
				return
			}
		}
		if err := c.watcher.Rebuild(ctx, sourcePaths); err != nil {
			if ctx.Err() == nil {
				c.logger.Warn("sync usage transcript watcher failed", "err", err)
				scheduleRetry(refreshRetryID, c.now().UTC().Add(c.retryDelay))
			}
			return
		}
		for sourceID := range retries {
			if sourceID == refreshRetryID {
				continue
			}
			if _, ok := live[sourceID]; !ok {
				delete(retries, sourceID)
			}
		}
		for sourceID := range unread {
			if _, ok := live[sourceID]; !ok {
				delete(unread, sourceID)
			}
		}
		paths = nextPaths
		pending, err := c.store.HasPendingUsageDiscovery(ctx)
		if err != nil {
			if ctx.Err() == nil {
				c.logger.Warn("check pending usage discovery failed", "err", err)
				scheduleRetry(refreshRetryID, c.now().UTC().Add(c.retryDelay))
			}
		} else if pending {
			if _, scheduled := retries[refreshRetryID]; !scheduled {
				scheduleRetry(refreshRetryID, c.now().UTC().Add(c.retryDelay))
			}
		} else {
			delete(retries, refreshRetryID)
		}
	}

	recoverWatcher := func(err error) {
		if err != nil {
			c.logger.Warn("usage transcript watcher failed; rebuilding", "err", err)
		}
		// Inventory refresh performs one rebuild with the latest durable paths;
		// discovery first recovers files created while fsnotify was unreliable.
		refreshInventory(true)
	}

	// Watch roots are active before reconciliation, closing the startup gap
	// between reading an offset and beginning to receive filesystem events.
	refreshInventory(true)

	events := c.watcher.Events()
	watcherErrors := c.watcher.Errors()
	for {
		if len(waiters) > 0 && len(ready) == 0 && running == 0 {
			for _, done := range waiters {
				close(done)
			}
			waiters = nil
		}
		var workOut chan int64
		var nextSourceID int64
		if len(ready) > 0 {
			workOut = work
			nextSourceID = ready[0]
		}
		retryC := armRetryTimer(&retryTimer, retries, c.now().UTC())

		select {
		case <-ctx.Done():
			if retryTimer != nil {
				stopTimer(retryTimer)
			}
			return
		case workOut <- nextSourceID:
			ready = ready[1:]
			running++
			state := states[nextSourceID]
			state.queued = false
			state.running = true
		case completed := <-results:
			running--
			state := states[completed.sourceID]
			if state == nil {
				state = &sourceWorkState{}
				states[completed.sourceID] = state
			}
			state.running = false
			if completed.err != nil && ctx.Err() == nil {
				c.logger.Warn("usage source ingestion failed", "source", completed.sourceID, "err", completed.err)
			}
			if completed.result.RetryAt != nil {
				scheduleRetry(completed.sourceID, *completed.result.RetryAt)
			} else if completed.err != nil && ctx.Err() == nil {
				scheduleRetry(completed.sourceID, c.now().UTC().Add(c.retryDelay))
			}
			if completed.result.SyncWatch {
				sourcePaths := make([]string, 0, len(paths))
				for path := range paths {
					sourcePaths = append(sourcePaths, path)
				}
				if err := c.watcher.Rebuild(ctx, sourcePaths); err != nil && ctx.Err() == nil {
					c.logger.Warn("restore usage transcript watch failed", "err", err)
					scheduleRetry(refreshRetryID, c.now().UTC().Add(c.retryDelay))
				}
			}
			if completed.result.ReconcilePath != "" && c.reconcilePath != nil {
				if err := c.reconcilePath(ctx, completed.result.ReconcilePath); err != nil && ctx.Err() == nil {
					c.logger.Warn("usage source path reconciliation failed", "err", err)
				} else {
					refreshInventory(false)
				}
			}
			if completed.result.Reconcile {
				refreshInventory(true)
			} else if completed.result.Refresh {
				refreshInventory(false)
			}
			if completed.result.ReplacementSourceID != 0 {
				enqueue(completed.result.ReplacementSourceID)
			}
			if completed.result.More {
				state.dirty = true
			}
			if state.dirty {
				state.dirty = false
				enqueue(completed.sourceID)
			}
		case event, ok := <-events:
			if !ok {
				c.logger.Warn("usage transcript watcher event channel closed")
				return
			}
			path := canonicalTranscriptPath(event.Path)
			sourceIDs := paths[path]
			if len(sourceIDs) == 0 {
				if c.reconcilePath != nil {
					if err := c.reconcilePath(ctx, path); err != nil && ctx.Err() == nil {
						c.logger.Warn("usage source path reconciliation failed", "err", err)
					} else {
						refreshInventory(false)
						sourceIDs = paths[path]
					}
				}
			}
			if len(sourceIDs) == 0 && event.Discovery {
				refreshInventory(true)
				sourceIDs = paths[path]
			}
			for _, sourceID := range sourceIDs {
				mark(sourceID)
			}
		case err, ok := <-watcherErrors:
			if !ok {
				c.logger.Warn("usage transcript watcher error channel closed")
				return
			}
			recoverWatcher(err)
		case <-c.refresh:
			refreshInventory(true)
		case <-c.inventory:
			refreshInventory(false)
		case done := <-c.flushes:
			waiters = append(waiters, done)
			for sourceID := range unread {
				enqueue(sourceID)
			}
		case <-retryC:
			now := c.now().UTC()
			for sourceID, at := range retries {
				if at.After(now) {
					continue
				}
				delete(retries, sourceID)
				if sourceID == refreshRetryID {
					recoverWatcher(nil)
					continue
				}
				enqueue(sourceID)
			}
		}
	}
}

func canonicalTranscriptPath(path string) string {
	path = filepath.Clean(strings.TrimSpace(path))
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	// Rename/remove events arrive after the leaf stopped existing. Resolve the
	// parent so aliases such as macOS /var and /private/var still identify the
	// same durable source.
	if parent, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
		return filepath.Join(parent, filepath.Base(path))
	}
	return path
}

func armRetryTimer(timer **time.Timer, retries map[int64]time.Time, now time.Time) <-chan time.Time {
	if *timer != nil {
		stopTimer(*timer)
	}
	var earliest time.Time
	for _, at := range retries {
		if earliest.IsZero() || at.Before(earliest) {
			earliest = at
		}
	}
	if earliest.IsZero() {
		return nil
	}
	delay := earliest.Sub(now)
	if delay < 0 {
		delay = 0
	}
	if *timer == nil {
		*timer = time.NewTimer(delay)
	} else {
		(*timer).Reset(delay)
	}
	return (*timer).C
}

func stopTimer(timer *time.Timer) {
	if timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}
