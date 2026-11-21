package usage

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

const (
	defaultWatcherRestartDelay = 30 * time.Second
	flushWait                  = 5 * time.Second
)

type watcherFactory func(context.Context, []string) (transcriptWatcher, error)

// Pipeline supervises the event-driven transcript watcher and coordinator.
// Durable cursors remain in SQLite, so recreating either component is safe.
type Pipeline struct {
	store       coordinatorStore
	ingestor    sourceIngestor
	roots       []string
	cfg         CoordinatorConfig
	logger      *slog.Logger
	newWatcher  watcherFactory
	restartWait time.Duration
	reconcile   chan struct{}
	inventory   chan struct{}
	live        atomic.Pointer[Coordinator]
}

// NewPipeline constructs a supervised usage collection pipeline.
func NewPipeline(
	store coordinatorStore,
	ingestor sourceIngestor,
	roots []string,
	cfg CoordinatorConfig,
) *Pipeline {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Pipeline{
		store:    store,
		ingestor: ingestor,
		roots:    append([]string(nil), roots...),
		cfg:      cfg,
		logger:   logger,
		newWatcher: func(ctx context.Context, roots []string) (transcriptWatcher, error) {
			return NewTranscriptWatcher(ctx, roots)
		},
		restartWait: defaultWatcherRestartDelay,
		reconcile:   make(chan struct{}, 1),
		inventory:   make(chan struct{}, 1),
	}
}

// NotifySourcesChanged requests bounded discovery.
func (p *Pipeline) NotifySourcesChanged() {
	select {
	case p.reconcile <- struct{}{}:
	default:
	}
}

// NotifyInventoryChanged records newly registered sources without scanning
// provider roots.
func (p *Pipeline) NotifyInventoryChanged() {
	select {
	case p.inventory <- struct{}{}:
	default:
	}
}

// Flush ingests unread usage changes before a read, waiting at most flushWait.
func (p *Pipeline) Flush(ctx context.Context) {
	if p == nil {
		return
	}
	coordinator := p.live.Load()
	if coordinator == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, flushWait)
	defer cancel()
	coordinator.Flush(ctx)
}

// Start runs until ctx is canceled, recreating a failed watcher after a bounded
// delay. The returned channel closes after the active coordinator has stopped.
func (p *Pipeline) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.run(ctx)
	}()
	return done
}

func (p *Pipeline) run(ctx context.Context) {
	for {
		watcher, err := p.newWatcher(ctx, p.roots)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			p.logger.Warn("usage transcript watcher unavailable; retrying", "err", err, "retry_in", p.restartWait)
			if !waitForPipelineRetry(ctx, p.restartWait) {
				return
			}
			continue
		}

		if !p.runCoordinator(ctx, watcher) {
			return
		}
		p.logger.Warn("usage transcript watcher stopped; restarting", "retry_in", p.restartWait)
		if !waitForPipelineRetry(ctx, p.restartWait) {
			return
		}
	}
}

func (p *Pipeline) runCoordinator(ctx context.Context, watcher transcriptWatcher) bool {
	coordinatorCtx, cancelCoordinator := context.WithCancel(ctx)
	defer cancelCoordinator()
	coordinator := NewCoordinator(p.store, p.ingestor, watcher, p.cfg)
	coordinatorDone := coordinator.Start(coordinatorCtx)
	p.live.Store(coordinator)
	defer p.live.CompareAndSwap(coordinator, nil)
	for {
		select {
		case <-ctx.Done():
			cancelCoordinator()
			<-coordinatorDone
			return false
		case <-coordinatorDone:
			return ctx.Err() == nil
		case <-p.reconcile:
			coordinator.NotifySourcesChanged()
		case <-p.inventory:
			coordinator.NotifyInventoryChanged()
		}
	}
}

func waitForPipelineRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
