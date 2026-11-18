package cdc

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// DefaultPollInterval is how often the poller checks change_log for new rows.
// With a Wake channel it is the shortest time between polls instead.
const DefaultPollInterval = 100 * time.Millisecond

// wakeFallbackInterval is how often a poller with a Wake channel polls without
// a signal, for change_log rows written outside the store's write lock.
const wakeFallbackInterval = time.Second

// DefaultBatch bounds how many events one poll drains.
const DefaultBatch = 512

// Source is the poller's view of the durable log: read events after a seq, and
// the current head seq. The storage layer implements it (the change_log table).
type Source interface {
	EventsAfter(ctx context.Context, after int64, limit int) ([]Event, error)
	LatestSeq(ctx context.Context) (int64, error)
}

// Poller tails change_log and fans each new event out through the Broadcaster,
// in seq order. It holds only an in-memory cursor (lastSeq): it is the LIVE push
// path, while durable catch-up is the client's job (read change_log from its own
// offset). A restart re-seeks to head, so the poller never re-broadcasts history
// to a freshly-started broadcaster.
type Poller struct {
	src      Source
	bcast    *Broadcaster
	interval time.Duration
	wake     <-chan struct{}
	fallback time.Duration
	batch    int
	logger   *slog.Logger
	lastSeq  int64
}

// PollerConfig holds optional knobs; zero values fall back to defaults. StartSeq
// is the cursor to begin from; production wiring leaves it 0 and calls
// SeekToHead, tests set it to read from the beginning.
type PollerConfig struct {
	Interval time.Duration
	// Wake signals a committed write. When set, the poller polls on it and once
	// a second without it, instead of every Interval.
	Wake     <-chan struct{}
	Batch    int
	Logger   *slog.Logger
	StartSeq int64
}

// NewPoller constructs a Poller over src, fanning out through bcast.
func NewPoller(src Source, bcast *Broadcaster, cfg PollerConfig) *Poller {
	p := &Poller{
		src:      src,
		bcast:    bcast,
		interval: cfg.Interval,
		wake:     cfg.Wake,
		fallback: wakeFallbackInterval,
		batch:    cfg.Batch,
		logger:   cfg.Logger,
		lastSeq:  cfg.StartSeq,
	}
	if p.interval <= 0 {
		p.interval = DefaultPollInterval
	}
	if p.batch <= 0 {
		p.batch = DefaultBatch
	}
	if p.logger == nil {
		p.logger = slog.Default()
	}
	return p
}

// SeekToHead moves the cursor to the current head, so the poller only broadcasts
// events created from now on (clients catch up on older events via the store).
func (p *Poller) SeekToHead(ctx context.Context) error {
	seq, err := p.src.LatestSeq(ctx)
	if err != nil {
		return fmt.Errorf("cdc poller seek: %w", err)
	}
	p.lastSeq = seq
	return nil
}

// Start runs the poll loop until ctx is cancelled; the returned channel closes
// when the loop has exited.
func (p *Poller) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := p.interval
		if p.wake != nil {
			tick = p.fallback
		}
		t := time.NewTicker(tick)
		defer t.Stop()
		again := false
		var last time.Time
		for {
			if !again {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				case <-p.wake:
				}
			}
			// Space polls during a burst of writes. Waiting before the poll, not
			// after it, keeps an idle poller from waking a second time.
			if wait := p.interval - time.Since(last); p.wake != nil && wait > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(wait):
				}
			}
			if ctx.Err() != nil {
				return
			}
			// The read below sees every write committed before it, so a signal
			// already pending is covered; one arriving during the read stays queued.
			drained := false
			select {
			case <-p.wake:
				drained = true
			default:
			}
			last = time.Now()
			n, err := p.poll(ctx)
			if err != nil {
				p.logger.Error("cdc poller: poll failed", "err", err)
			}
			// A full batch reads on at once; a failed read retries once for the
			// signal it consumed.
			again = (err == nil && n == p.batch) || (err != nil && drained)
		}
	}()
	return done
}

// Poll drains one batch of new events and broadcasts them in seq order,
// advancing the cursor. Exported so tests (and a daemon) can drive a cycle
// synchronously.
func (p *Poller) Poll(ctx context.Context) error {
	_, err := p.poll(ctx)
	return err
}

// poll returns how many events it read.
func (p *Poller) poll(ctx context.Context) (int, error) {
	evs, err := p.src.EventsAfter(ctx, p.lastSeq, p.batch)
	if err != nil {
		return 0, fmt.Errorf("cdc poller: read after %d: %w", p.lastSeq, err)
	}
	for _, e := range evs {
		if e.Seq <= p.lastSeq {
			continue // idempotent guard
		}
		p.bcast.Publish(e)
		p.lastSeq = e.Seq
	}
	return len(evs), nil
}

// LastSeq returns the poller's current cursor (the highest seq broadcast).
func (p *Poller) LastSeq() int64 { return p.lastSeq }
