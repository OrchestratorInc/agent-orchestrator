package daemon

import (
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/cdc"
)

// sessionChanges wakes a resting loop for each session created or updated,
// coalesced, and ignores every other event.
func TestSessionChangesSignalsSessionEventsCoalesced(t *testing.T) {
	bcast := cdc.NewBroadcaster()
	signal := (&cdcPipeline{Broadcaster: bcast}).sessionChanges()

	bcast.Publish(cdc.Event{Type: cdc.EventPRUpdated})
	select {
	case <-signal:
		t.Fatal("a PR event signalled a session change")
	default:
	}

	bcast.Publish(cdc.Event{Type: cdc.EventSessionCreated})
	bcast.Publish(cdc.Event{Type: cdc.EventSessionUpdated})
	select {
	case <-signal:
	default:
		t.Fatal("session events did not signal")
	}
	select {
	case <-signal:
		t.Fatal("two session events signalled twice; want one coalesced signal")
	default:
	}

	bcast.Publish(cdc.Event{Type: cdc.EventSessionUpdated})
	select {
	case <-signal:
	default:
		t.Fatal("a session update after the signal was read did not signal again")
	}
}
