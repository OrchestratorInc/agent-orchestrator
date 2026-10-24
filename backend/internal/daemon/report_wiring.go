package daemon

import (
	"encoding/json"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// reportActivityObserver is the report coordinator's session-stop hook.
type reportActivityObserver interface {
	SessionActivityChanged(domain.ActivityState, bool)
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
