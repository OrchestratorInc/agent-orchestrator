package daemon

import (
	"context"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// cdcPipeline owns the running CDC poller and live-event broadcaster. The DB
// triggers write change_log; the poller tails it and fans each new event out to
// live transports such as terminal session-state subscriptions. Durable catch-up
// is a client concern; the poller only pushes live events and re-seeks to head
// on restart. It also runs the provider-event archive janitor, so the same Stop
// waits for both background deletes before the store closes.
type cdcPipeline struct {
	Broadcaster          *cdc.Broadcaster
	done                 <-chan struct{}
	retentionDone        <-chan struct{}
	archiveRetentionDone <-chan struct{}
}

// archiveRetentionConfig is the archive janitor's configuration; production uses
// the defaults, and tests shorten the first-pass delay.
var archiveRetentionConfig chatsvc.ArchiveRetentionConfig

// startCDC seeks the poller to the current head and starts its loop. It stops
// when ctx is cancelled; Stop waits for it to drain.
func startCDC(ctx context.Context, store *sqlite.Store, logger *slog.Logger) (*cdcPipeline, error) {
	bcast := cdc.NewBroadcaster()
	poller := cdc.NewPoller(store, bcast, cdc.PollerConfig{Logger: logger})
	if err := poller.SeekToHead(ctx); err != nil {
		return nil, err
	}
	janitor := cdc.NewRetentionJanitor(store, cdc.RetentionConfig{Logger: logger})
	archiveConfig := archiveRetentionConfig
	archiveConfig.Logger = logger
	archiveJanitor := chatsvc.NewArchiveRetention(store, archiveConfig)
	return &cdcPipeline{
		Broadcaster:          bcast,
		done:                 poller.Start(ctx),
		retentionDone:        janitor.Start(ctx),
		archiveRetentionDone: archiveJanitor.Start(ctx),
	}, nil
}

// Stop waits for the poller goroutine to exit (the caller must have cancelled the
// ctx passed to startCDC).
func (p *cdcPipeline) Stop() error {
	<-p.done
	if p.retentionDone != nil {
		<-p.retentionDone
	}
	if p.archiveRetentionDone != nil {
		<-p.archiveRetentionDone
	}
	return nil
}
