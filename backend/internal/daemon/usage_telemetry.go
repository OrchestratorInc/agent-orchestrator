package daemon

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/deliverytelemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	sqlitestore "github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

const usageFlushInterval = 30 * time.Minute

// usageTelemetryFlusher only exports bounded, durable local counts. Emission
// reserves each summary before handing it to the existing best-effort sink.
type usageTelemetryFlusher struct {
	mu       sync.Mutex
	store    *sqlite.Store
	sink     ports.EventSink
	identity ports.TelemetryIdentityStore
	// milestones backs first_for_install on new PR outcomes. Nil omits it.
	milestones ports.InstallMilestones
	log        *slog.Logger
	archive    chan struct{}
}

func startUsageTelemetry(ctx context.Context, store *sqlite.Store, sink ports.EventSink, identity ports.TelemetryIdentityStore, milestones ports.InstallMilestones, log *slog.Logger) *usageTelemetryFlusher {
	f := &usageTelemetryFlusher{store: store, sink: sink, identity: identity, milestones: milestones, log: log, archive: make(chan struct{}, 1)}
	go func() {
		f.flush(ctx, false)
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
			case <-f.archive:
				f.flush(ctx, true)
			}
		}
	}()
	return f
}

func (f *usageTelemetryFlusher) requestArchiveFlush() {
	select {
	case f.archive <- struct{}{}:
	default:
	}
}

func (f *usageTelemetryFlusher) flush(ctx context.Context, terminatedOnly bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.identity == nil || f.identity.OptedOut() {
		if err := f.store.DiscardUsageMessages(ctx); err != nil {
			f.log.Warn("discard usage messages", "error", err)
		}
		if err := f.store.DiscardPendingWorkerPRUsage(ctx); err != nil {
			f.log.Warn("discard usage prs", "error", err)
		}
		return
	}
	if f.identity.ExportSuppressed() {
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
			if f.identity.ExportSuppressed() {
				return
			}
			f.sink.Emit(ctx, ports.TelemetryEvent{
				Name: "ao.usage.messages_sent", Source: "daemon", OccurredAt: time.Now().UTC(),
				Level: ports.TelemetryLevelInfo,
				Payload: map[string]any{
					"project_telemetry_id": b.ProjectTelemetryID,
					"session_telemetry_id": deliverytelemetry.SessionTelemetryID(b.ProjectTelemetryID, b.SessionID),
					// count keeps its first meaning (direct human Chat
					// messages); the rest add the other human sends and split
					// all of them by client.
					"count":         b.Count,
					"steer_count":   b.SteerCount,
					"send_count":    b.SendCount,
					"desktop_count": b.DesktopCount,
					"mobile_count":  b.MobileCount,
					"cli_count":     b.CLICount,
					"kind":          telemetrymeta.SessionKind(domain.SessionRecord{ProjectID: domain.ProjectID(b.ProjectID), Kind: domain.SessionKind(b.Kind)}),
					"harness":       b.Harness,
				},
			})
		}
		if len(batches) < 1000 {
			break
		}
	}
	if !terminatedOnly {
		f.flushPRs(ctx, installID)
	}
}

func (f *usageTelemetryFlusher) flushPRs(ctx context.Context, installID string) {
	// Whether anything was measured before this flush decides first_for_install.
	// The flusher holds f.mu, so no other report can interleave.
	associatedBefore, mergedBefore, err := f.store.WorkerPRUsageHistory(ctx)
	historyKnown := err == nil
	if err != nil {
		f.log.Warn("read worker pr usage history", "error", err)
	}
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
			kind       string
		}
		// Historical backfill stays grouped: it has no meaningful timing. New
		// outcomes go out one per PR with their detail and count 1, so summing
		// count still gives the same totals.
		counts := make(map[group]int64)
		var detailed []ports.TelemetryEvent
		projectIDs := make(map[string]string)
		processed := 0
		for _, fact := range facts {
			if f.identity.ExportSuppressed() {
				return
			}
			projectTelemetryID, ok := projectIDs[fact.ProjectID]
			if !ok {
				var err error
				projectTelemetryID, err = f.store.UsageProjectID(ctx, fact.ProjectID, installID)
				if err != nil {
					f.log.Warn("usage project identity", "error", err)
					continue
				}
				projectIDs[fact.ProjectID] = projectTelemetryID
			}
			closePending := fact.ClosePending && !fact.Merged
			if err := f.store.MarkWorkerPRUsageReported(ctx, fact.Key, fact.AssociationPending, fact.MergePending, closePending); err != nil {
				f.log.Warn("mark worker pr usage", "error", err)
				continue
			}
			processed++
			if fact.AssociationPending {
				if fact.Historical {
					counts[group{projectTelemetryID, "ao.usage.worker_pr_associated", true, workerPRKind(fact)}]++
				} else {
					payload := workerPRDetail(projectTelemetryID, fact)
					if first, known := f.firstForInstall("pr_opened", historyKnown, !associatedBefore); known {
						payload["first_for_install"] = first
					}
					if ms, ok := elapsedMs(fact.SessionCreatedAt, fact.PRCreatedAt); ok {
						payload["ms_since_spawn"] = ms
					}
					detailed = append(detailed, usageEvent("ao.usage.worker_pr_associated", payload))
				}
				associatedBefore = true
			}
			if fact.MergePending {
				if fact.HistoricalMerged {
					counts[group{projectTelemetryID, "ao.usage.worker_pr_merged", true, workerPRKind(fact)}]++
				} else {
					payload := workerPROutcome(projectTelemetryID, fact, fact.MergedAt)
					mergedVia := fact.MergedVia
					if mergedVia == "" {
						mergedVia = deliverytelemetry.MergedOutsideAO(fact.Provider)
					}
					if mergedVia != "" {
						payload["merged_via"] = mergedVia
					}
					if first, known := f.firstForInstall("pr_merged", historyKnown, !mergedBefore); known {
						payload["first_for_install"] = first
					}
					detailed = append(detailed, usageEvent("ao.usage.worker_pr_merged", payload))
				}
				mergedBefore = true
			}
			if closePending {
				detailed = append(detailed, usageEvent("ao.prs.closed", workerPROutcome(projectTelemetryID, fact, fact.ClosedAt)))
			}
		}
		for key, count := range counts {
			if f.identity.ExportSuppressed() {
				return
			}
			payload := map[string]any{"project_telemetry_id": key.project, "count": count, "historical": key.historical}
			if key.kind != "" {
				payload["kind"] = key.kind
			}
			f.sink.Emit(ctx, usageEvent(key.event, payload))
		}
		for _, ev := range detailed {
			if f.identity.ExportSuppressed() {
				return
			}
			f.sink.Emit(ctx, ev)
		}
		if len(facts) < 1000 || processed == 0 {
			return
		}
	}
}

// firstForInstall claims a PR milestone once per install. noPrior is whether
// nothing was measured before; with an unreadable history the flag is omitted
// and the marker left unclaimed.
func (f *usageTelemetryFlusher) firstForInstall(name string, historyKnown, noPrior bool) (first, known bool) {
	if !historyKnown {
		return false, false
	}
	return ports.FirstForInstall(f.milestones, name, noPrior)
}

func usageEvent(name string, payload map[string]any) ports.TelemetryEvent {
	return ports.TelemetryEvent{Name: name, Source: "daemon", OccurredAt: time.Now().UTC(), Level: ports.TelemetryLevelInfo, Payload: payload}
}

// workerPRDetail is the shared payload of a new (non-historical) worker PR
// outcome: counts, enums and the owning worker's harness, never PR text.
func workerPRDetail(projectTelemetryID string, fact sqlitestore.UsagePRFact) map[string]any {
	payload := map[string]any{"project_telemetry_id": projectTelemetryID, "count": 1, "historical": false}
	if kind := workerPRKind(fact); kind != "" {
		payload["kind"] = kind
	}
	if fact.Harness != "" {
		payload["harness"] = fact.Harness
	}
	if fact.Provider == "github" || fact.Provider == "gitlab" {
		payload["scm_provider"] = fact.Provider
	}
	return payload
}

func workerPRKind(fact sqlitestore.UsagePRFact) string {
	if fact.SessionKind == "" {
		return ""
	}
	return telemetrymeta.SessionKind(domain.SessionRecord{ProjectID: domain.ProjectID(fact.ProjectID), Kind: domain.SessionKind(fact.SessionKind)})
}

// workerPROutcome adds timing and review facts for a merge or close at at.
func workerPROutcome(projectTelemetryID string, fact sqlitestore.UsagePRFact, at time.Time) map[string]any {
	payload := workerPRDetail(projectTelemetryID, fact)
	payload["review_rounds"] = fact.ReviewRounds
	payload["ao_review_used"] = fact.AOReviewUsed
	if ms, ok := elapsedMs(fact.SessionCreatedAt, at); ok {
		payload["ms_since_spawn"] = ms
	}
	if ms, ok := elapsedMs(fact.PRCreatedAt, at); ok {
		payload["ms_since_open"] = ms
	}
	return payload
}

// elapsedMs is to-from in milliseconds when both are known; a negative span
// (clock skew between AO and the provider) reads as zero.
func elapsedMs(from, to time.Time) (int64, bool) {
	if from.IsZero() || to.IsZero() {
		return 0, false
	}
	ms := to.Sub(from).Milliseconds()
	if ms < 0 {
		ms = 0
	}
	return ms, true
}
