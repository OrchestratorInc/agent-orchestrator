package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

const usageFlushInterval = 30 * time.Minute

// usageTelemetryFlusher only exports bounded, durable local counts. Emission
// reserves each summary before handing it to the existing best-effort sink.
type usageTelemetryFlusher struct {
	mu       sync.Mutex
	store    *sqlite.Store
	sink     ports.EventSink
	identity ports.TelemetryIdentityStore
	log      *slog.Logger
}

func startUsageTelemetry(ctx context.Context, store *sqlite.Store, sink ports.EventSink, identity ports.TelemetryIdentityStore, log *slog.Logger) *usageTelemetryFlusher {
	f := &usageTelemetryFlusher{store: store, sink: sink, identity: identity, log: log}
	store.SetUsageTelemetryAllowed(func() bool { return identity != nil && !identity.Snapshot().OptedOut })
	f.flush(context.Background(), false)
	go func() {
		minute := time.NewTicker(time.Minute)
		full := time.NewTicker(usageFlushInterval)
		defer minute.Stop()
		defer full.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-minute.C:
				f.flush(ctx, true)
			case <-full.C:
				f.flush(ctx, false)
			}
		}
	}()
	return f
}

func (f *usageTelemetryFlusher) flush(ctx context.Context, terminatedOnly bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.identity == nil || f.identity.Snapshot().OptedOut {
		if err := f.store.DiscardUsageMessages(ctx); err != nil {
			f.log.Warn("discard usage messages", "error", err)
		}
		if err := f.store.DiscardPendingWorkerPRUsage(ctx); err != nil {
			f.log.Warn("discard usage prs", "error", err)
		}
		return
	}
	installID := f.identity.Snapshot().InstallID
	if installID == "" {
		return
	}
	for {
		batches, err := f.store.DrainUsageMessages(ctx, 1000, terminatedOnly, installID)
		if err != nil {
			f.log.Warn("drain usage messages", "error", err)
			return
		}
		for _, b := range batches {
			if f.identity.Snapshot().OptedOut {
				return
			}
			sessionKey := sha256.Sum256([]byte(b.ProjectTelemetryID + ":" + b.SessionID))
			f.sink.Emit(ctx, ports.TelemetryEvent{
				Name: "ao.usage.messages_sent", Source: "daemon", OccurredAt: time.Now().UTC(),
				Level: ports.TelemetryLevelInfo,
				Payload: map[string]any{
					"project_telemetry_id": b.ProjectTelemetryID,
					"session_telemetry_id": hex.EncodeToString(sessionKey[:16]),
					"count":                b.Count,
				},
			})
		}
		if len(batches) < 1000 {
			break
		}
	}
	f.flushPRs(ctx, installID)
}

func (f *usageTelemetryFlusher) flushPRs(ctx context.Context, installID string) {
	for {
		facts, err := f.store.PendingWorkerPRUsage(ctx, 1000)
		if err != nil {
			f.log.Warn("read worker pr usage", "error", err)
			return
		}
		type group struct {
			project    string
			event      string
			historical bool
		}
		counts := make(map[group]int64)
		processed := 0
		for _, fact := range facts {
			if f.identity.Snapshot().OptedOut {
				return
			}
			projectTelemetryID, err := f.store.UsageProjectID(ctx, fact.ProjectID, installID)
			if err != nil {
				f.log.Warn("usage project identity", "error", err)
				continue
			}
			if err := f.store.MarkWorkerPRUsageReported(ctx, fact.Key, fact.AssociationPending, fact.MergePending); err != nil {
				f.log.Warn("mark worker pr usage", "error", err)
				continue
			}
			processed++
			if fact.AssociationPending {
				counts[group{projectTelemetryID, "ao.usage.worker_pr_associated", fact.Historical}]++
			}
			if fact.MergePending {
				counts[group{projectTelemetryID, "ao.usage.worker_pr_merged", fact.HistoricalMerged}]++
			}
		}
		for key, count := range counts {
			if f.identity.Snapshot().OptedOut {
				return
			}
			f.sink.Emit(ctx, ports.TelemetryEvent{
				Name: key.event, Source: "daemon", OccurredAt: time.Now().UTC(),
				Level:   ports.TelemetryLevelInfo,
				Payload: map[string]any{"project_telemetry_id": key.project, "count": count, "historical": key.historical},
			})
		}
		if len(facts) < 1000 || processed == 0 {
			return
		}
	}
}
