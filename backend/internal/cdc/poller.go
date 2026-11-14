package cdc

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

const DefaultPollInterval = 100 * time.Millisecond

const DefaultBatch = 512

type Source interface {
	EventsAfter(ctx context.Context, after int64, limit int) ([]Event, error)
	LatestSeq(ctx context.Context) (int64, error)
}

type Poller struct {
	src      Source
	bcast    *Broadcaster
	interval time.Duration
	batch    int
	logger   *slog.Logger
	lastSeq  int64
}

type PollerConfig struct {
	Interval time.Duration
	Batch    int
	Logger   *slog.Logger
	StartSeq int64
}

func NewPoller(src Source, bcast *Broadcaster, cfg PollerConfig) *Poller {
	p := &Poller{
		src:      src,
		bcast:    bcast,
		interval: cfg.Interval,
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

func (p *Poller) SeekToHead(ctx context.Context) error {
	seq, err := p.src.LatestSeq(ctx)
	if err != nil {
		return fmt.Errorf("cdc poller seek: %w", err)
	}
	p.lastSeq = seq
	return nil
}

func (p *Poller) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(p.interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := p.Poll(ctx); err != nil {
					p.logger.Error("cdc poller: poll failed", "err", err)
				}
			}
		}
	}()
	return done
}

func (p *Poller) Poll(ctx context.Context) error {
	evs, err := p.src.EventsAfter(ctx, p.lastSeq, p.batch)
	if err != nil {
		return fmt.Errorf("cdc poller: read after %d: %w", p.lastSeq, err)
	}
	for _, e := range evs {
		if e.Seq <= p.lastSeq {
			continue
		}
		p.bcast.Publish(e)
		p.lastSeq = e.Seq
	}
	return nil
}

func (p *Poller) LastSeq() int64 { return p.lastSeq }
