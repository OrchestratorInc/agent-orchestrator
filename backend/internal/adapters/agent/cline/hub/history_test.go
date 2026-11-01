package hub

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestCorrelateNativeWireReplayAfterPIDMutation(t *testing.T) {
	// Captured from Cline 3.0.74 / SDK 0.2.0 with real AO-supervised child
	// processes and no prompts/model calls. Only ownership fields remain.
	raw, err := os.ReadFile("testdata/native-replay.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Root        string  `json:"root"`
		Since       int64   `json:"since"`
		Through     int64   `json:"through"`
		ExpectedPID int     `json:"expectedPID"`
		MutatedPID  int     `json:"mutatedPID"`
		HookAt      int64   `json:"hookAt"`
		Events      []Event `json:"events"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	binding, ok := Correlate(fixture.Events, fixture.Since, fixture.Through, fixture.Root, time.UnixMilli(fixture.HookAt))
	if !ok || binding.PID != fixture.ExpectedPID || binding.PID == fixture.MutatedPID {
		t.Fatalf("native replay = (%+v, %v), want original PID %d rather than changed PID %d", binding, ok, fixture.ExpectedPID, fixture.MutatedPID)
	}
}

func historyEvent(sequence int64, name, session string, payload any) Event {
	raw, _ := json.Marshal(payload)
	return Event{Version: "v1", Name: name, Sequence: sequence, SessionID: session, Timestamp: 1000 + sequence, Payload: raw}
}

func registration(sequence int64, client string, pid int) Event {
	return historyEvent(sequence, "hub.client.registered", "", map[string]any{
		"clientId": client, "connectedAt": 1000 + sequence,
		"metadata": map[string]any{"pid": pid, "version": "0.2.0"},
	})
}

func created(sequence int64, root, creator string) Event {
	return historyEvent(sequence, "session.created", root, map[string]any{
		"session": map[string]any{"sessionId": root, "createdByClientId": creator},
	})
}

func TestCorrelateSharedWorkspaceUsesOriginalRegistration(t *testing.T) {
	events := []Event{
		registration(1, "worker", 101), registration(2, "reviewer", 202),
		created(3, "worker-root", "worker"), created(4, "review-root", "reviewer"),
		historyEvent(5, "hub.client.updated", "", map[string]any{
			"clientId": "worker", "metadata": map[string]any{"pid": 202},
		}),
	}
	for root, want := range map[string]int{"worker-root": 101, "review-root": 202} {
		binding, ok := Correlate(events, 0, 5, root, time.UnixMilli(2000))
		if !ok || binding.PID != want {
			t.Fatalf("root %q = (%+v, %v), want original PID %d", root, binding, ok, want)
		}
	}
}

func TestCorrelateRejectsIncompleteOrAmbiguousHistory(t *testing.T) {
	baseline := []Event{registration(1, "worker", 101), created(2, "root", "worker")}
	tests := []struct {
		name   string
		events []Event
		since  int64
		end    int64
		at     int64
	}{
		{"missing registration", baseline[1:], 1, 2, 2000},
		{"pruned prefix", baseline[1:], 0, 2, 2000},
		{"missing tail", baseline[:1], 0, 2, 2000},
		{"missing middle", []Event{registration(1, "worker", 101), created(3, "root", "worker")}, 0, 3, 2000},
		{"duplicate sequence", []Event{registration(1, "worker", 101), created(1, "root", "worker")}, 0, 2, 2000},
		{"late hook", baseline, 0, 2, 1001},
		{"same millisecond", baseline, 0, 2, 1002},
		{"no authored time", baseline, 0, 2, 0},
		{"unbounded cursor", baseline, 0, MaxEvents + 1, 2000},
		{"duplicate live client", append(append([]Event{}, baseline...), registration(3, "worker", 202)), 0, 3, 2000},
		{"disconnected before creation", []Event{registration(1, "worker", 101), historyEvent(2, "hub.client.disconnected", "", map[string]any{"clientId": "worker"}), created(3, "root", "worker")}, 0, 3, 2000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if binding, ok := Correlate(tt.events, tt.since, tt.end, "root", time.UnixMilli(tt.at)); ok {
				t.Fatalf("ambiguous history attributed as %+v", binding)
			}
		})
	}
	for _, change := range []struct {
		name string
		edit func([]Event)
	}{
		{"version drift", func(e []Event) { e[0].Version = "v2" }},
		{"malformed payload", func(e []Event) { e[0].Payload = json.RawMessage(`{`) }},
		{"wrong root envelope", func(e []Event) { e[1].SessionID = "other" }},
		{"missing timestamp", func(e []Event) { e[0].Timestamp = 0 }},
	} {
		t.Run(change.name, func(t *testing.T) {
			events := append([]Event{}, baseline...)
			change.edit(events)
			if binding, ok := Correlate(events, 0, 2, "root", time.UnixMilli(2000)); ok {
				t.Fatalf("invalid history attributed as %+v", binding)
			}
		})
	}
}

func TestCorrelateDisconnectRetainsOnlyHistoricalCandidate(t *testing.T) {
	events := []Event{
		registration(1, "worker", 101), created(2, "root", "worker"),
		historyEvent(3, "hub.client.disconnected", "", map[string]any{"clientId": "worker"}),
		historyEvent(4, "session.attached", "root", map[string]any{"clientId": "reviewer"}),
	}
	binding, ok := Correlate(events, 0, 4, "root", time.UnixMilli(2000))
	if !ok || binding.PID != 101 {
		t.Fatalf("historical candidate = (%+v, %v), want original process pending live proof", binding, ok)
	}
}

func TestCorrelateRestoreRequiresCallbackAfterNewCreation(t *testing.T) {
	events := []Event{
		registration(1, "worker-old", 101), created(2, "root", "worker-old"),
		registration(3, "worker-new", 303), created(4, "root", "worker-new"),
	}
	if binding, ok := Correlate(events, 0, 4, "root", time.UnixMilli(1003)); ok {
		t.Fatalf("old hook relabeled as new generation: %+v", binding)
	}
	binding, ok := Correlate(events, 0, 4, "root", time.UnixMilli(2000))
	if !ok || binding.PID != 303 || binding.CreationSequence != 4 {
		t.Fatalf("new callback = (%+v, %v), want replacement process", binding, ok)
	}
}
