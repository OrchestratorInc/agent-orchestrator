package automations

import (
	"context"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/observe"
)

const DefaultTickInterval = 15 * time.Second

type Poller interface {
	Tick(context.Context, time.Time) error
}

type Config struct {
	Tick   time.Duration
	Clock  func() time.Time
	Logger *slog.Logger
}

type Observer struct {
	poller Poller
	tick   time.Duration
	clock  func() time.Time
	logger *slog.Logger
}

func New(poller Poller, cfg Config) *Observer {
	o := &Observer{poller: poller, tick: cfg.Tick, clock: cfg.Clock, logger: cfg.Logger}
	if o.tick <= 0 {
		o.tick = DefaultTickInterval
	}
	if o.clock == nil {
		o.clock = time.Now
	}
	if o.logger == nil {
		o.logger = slog.Default()
	}
	return o
}

func (o *Observer) Start(ctx context.Context) <-chan struct{} {
	return observe.StartPollLoop(ctx, o.tick, o.Poll, o.logger, "automations")
}

func (o *Observer) Poll(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if o.poller == nil {
		return nil
	}
	return o.poller.Tick(ctx, o.clock().UTC())
}
