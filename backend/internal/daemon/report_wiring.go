package daemon

import (
	"context"
	"encoding/json"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// reportActivityObserver is the report coordinator's session-stop hook.
type reportActivityObserver interface {
	SessionActivityChanged(domain.ActivityState, bool)
}

func startReportCoordinator(ctx context.Context, broadcaster *cdc.Broadcaster, coordinator interface {
	reportActivityObserver
	Start(context.Context) <-chan struct{}
}) (<-chan struct{}, func()) {
	// Start scans immediately, so worker stops must already reach the coordinator.
	unsubscribe := broadcaster.Subscribe(forwardReportActivity(coordinator))
	return coordinator.Start(ctx), unsubscribe
}

// forwardReportActivity relays session activity and termination changes from
// the CDC feed to the report coordinator so a pending done batch can flush as
// soon as the project's last working worker stops.
func forwardReportActivity(observer reportActivityObserver) func(cdc.Event) {
	return func(e cdc.Event) {
		if e.Type != cdc.EventSessionUpdated {
			return
		}
		var payload struct {
			Activity     domain.ActivityState `json:"activity"`
			IsTerminated bool                 `json:"isTerminated"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return
		}
		observer.SessionActivityChanged(payload.Activity, payload.IsTerminated)
	}
}

// workerReportObserver turns session activity changes into worker reports.
type workerReportObserver interface {
	Observe(context.Context, domain.SessionID, domain.ActivityState, bool, time.Time)
}

// startWorkerReporter subscribes the worker reporter to the CDC feed.
func startWorkerReporter(ctx context.Context, broadcaster *cdc.Broadcaster, reporter workerReportObserver) func() {
	return broadcaster.Subscribe(forwardWorkerReports(ctx, reporter))
}

func forwardWorkerReports(ctx context.Context, reporter workerReportObserver) func(cdc.Event) {
	return func(e cdc.Event) {
		if e.Type != cdc.EventSessionUpdated {
			return
		}
		var payload struct {
			ID           domain.SessionID     `json:"id"`
			Activity     domain.ActivityState `json:"activity"`
			IsTerminated bool                 `json:"isTerminated"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return
		}
		id := domain.SessionID(e.SessionID)
		if id == "" {
			id = payload.ID
		}
		reporter.Observe(ctx, id, payload.Activity, payload.IsTerminated, e.CreatedAt)
	}
}
