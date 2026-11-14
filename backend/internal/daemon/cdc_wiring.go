package daemon

import (
	"context"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

type cdcPipeline struct {
	Broadcaster   *cdc.Broadcaster
	done          <-chan struct{}
	retentionDone <-chan struct{}
}

func startCDC(ctx context.Context, store *sqlite.Store, logger *slog.Logger) (*cdcPipeline, error) {
	bcast := cdc.NewBroadcaster()
	poller := cdc.NewPoller(store, bcast, cdc.PollerConfig{Logger: logger})
	if err := poller.SeekToHead(ctx); err != nil {
		return nil, err
	}
	janitor := cdc.NewRetentionJanitor(store, cdc.RetentionConfig{Logger: logger})
	return &cdcPipeline{
		Broadcaster:   bcast,
		done:          poller.Start(ctx),
		retentionDone: janitor.Start(ctx),
	}, nil
}

func (p *cdcPipeline) Stop() error {
	<-p.done
	if p.retentionDone != nil {
		<-p.retentionDone
	}
	return nil
}
