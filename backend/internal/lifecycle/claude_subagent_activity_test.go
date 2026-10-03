package lifecycle

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestClaudeSubagentActivityKeepsSessionWorkingUntilLastChildStops(t *testing.T) {
	store := newFakeStore()
	start := time.Date(2026, 10, 3, 18, 4, 40, 0, time.UTC)
	store.sessions["ao-1"] = domain.SessionRecord{
		ID: "ao-1", Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: start},
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-1"},
	}
	m := New(store, nil)
	apply := func(second int, event, child string, state domain.ActivityState, running *[]string, want domain.ActivityState) {
		t.Helper()
		err := m.ApplyActivitySignal(context.Background(), "ao-1", ports.ActivitySignal{
			Valid: state != "", State: state, Event: event, SubagentID: child,
			RunningSubagentIDs: running, LaunchID: "launch-1", AgentSessionID: "native-1",
			Timestamp: start.Add(time.Duration(second) * time.Second),
		})
		if err != nil {
			t.Fatalf("%s: %v", event, err)
		}
		if got := store.sessions["ao-1"].Activity.State; got != want {
			t.Fatalf("after %s for %q: activity=%q, want %q", event, child, got, want)
		}
	}
	apply(1, "user-prompt-submit", "", domain.ActivityActive, nil, domain.ActivityActive)
	apply(2, "subagent-start", "child-1", "", nil, domain.ActivityActive)
	apply(3, "pre-tool-use", "child-1", domain.ActivityActive, nil, domain.ActivityActive)
	children := []string{"child-1"}
	apply(8, "stop", "", domain.ActivityIdle, &children, domain.ActivityActive)
	apply(14, "post-tool-use", "child-1", domain.ActivityActive, nil, domain.ActivityActive)
	// A new lifecycle Manager simulates a daemon restart. It reads the retained
	// parent and child facts rather than relying on its old in-memory map.
	m = New(store, nil)
	apply(16, "subagent-stop", "child-1", "", nil, domain.ActivityIdle)
	apply(17, "post-tool-use", "child-1", domain.ActivityActive, nil, domain.ActivityIdle)
	// A delayed Stop snapshot must not resurrect a completed child.
	apply(7, "stop", "", domain.ActivityIdle, &children, domain.ActivityIdle)
	// A delayed start for an unlisted child cannot override a newer empty snapshot.
	empty := []string{}
	apply(18, "stop", "", domain.ActivityIdle, &empty, domain.ActivityIdle)
	apply(15, "subagent-start", "unlisted-child", "", nil, domain.ActivityIdle)
}

func TestClaudeSubagentActivityWaitsForEveryChildAndPreservesPermissionBlock(t *testing.T) {
	store := newFakeStore()
	start := time.Date(2026, 10, 3, 18, 4, 40, 0, time.UTC)
	store.sessions["ao-1"] = domain.SessionRecord{
		ID: "ao-1", Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: start},
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-1"},
	}
	m := New(store, nil)
	apply := func(second int, event, child string, state domain.ActivityState, toolID string, running *[]string, want domain.ActivityState) {
		t.Helper()
		err := m.ApplyActivitySignal(context.Background(), "ao-1", ports.ActivitySignal{
			Valid: state != "", State: state, Event: event, SubagentID: child,
			ToolName: "Bash", ToolUseID: toolID, RunningSubagentIDs: running,
			LaunchID: "launch-1", AgentSessionID: "native-1",
			Timestamp: start.Add(time.Duration(second) * time.Second),
		})
		if err != nil {
			t.Fatalf("%s: %v", event, err)
		}
		if got := store.sessions["ao-1"].Activity.State; got != want {
			t.Fatalf("after %s for %q: activity=%q, want %q", event, child, got, want)
		}
	}
	apply(1, "user-prompt-submit", "", domain.ActivityActive, "", nil, domain.ActivityActive)
	apply(2, "subagent-start", "child-1", "", "", nil, domain.ActivityActive)
	apply(3, "subagent-start", "child-2", "", "", nil, domain.ActivityActive)
	children := []string{"child-1", "child-2"}
	apply(4, "stop", "", domain.ActivityIdle, "", &children, domain.ActivityActive)
	apply(5, "pre-tool-use", "child-1", domain.ActivityActive, "tool-1", nil, domain.ActivityActive)
	apply(6, "permission-request", "child-1", domain.ActivityBlocked, "", nil, domain.ActivityBlocked)
	apply(7, "subagent-stop", "child-2", "", "", nil, domain.ActivityBlocked)
	apply(8, "post-tool-use", "child-1", domain.ActivityActive, "tool-1", nil, domain.ActivityActive)
	apply(9, "subagent-stop", "child-1", "", "", nil, domain.ActivityIdle)
}

func TestClaudeSubagentActivityDropsPreviousLaunch(t *testing.T) {
	store := newFakeStore()
	start := time.Now().UTC()
	store.sessions["ao-1"] = domain.SessionRecord{
		ID: "ao-1", Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: start},
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-1"},
	}
	m := New(store, nil)
	for _, sig := range []ports.ActivitySignal{
		{Event: "subagent-start", SubagentID: "old-child", LaunchID: "launch-1", AgentSessionID: "native-1"},
		{Event: "stop", State: domain.ActivityIdle, Valid: true, LaunchID: "launch-1", AgentSessionID: "native-1"},
	} {
		if err := m.ApplyActivitySignal(context.Background(), "ao-1", sig); err != nil {
			t.Fatal(err)
		}
	}
	rec := store.sessions["ao-1"]
	rec.Metadata.RuntimeLaunchID = "launch-2"
	store.sessions["ao-1"] = rec
	if err := m.ApplyActivitySignal(context.Background(), "ao-1", ports.ActivitySignal{
		Event: "stop", State: domain.ActivityIdle, Valid: true,
		LaunchID: "launch-2", AgentSessionID: "native-2",
	}); err != nil {
		t.Fatal(err)
	}
	if got := store.sessions["ao-1"].Activity.State; got != domain.ActivityIdle {
		t.Fatalf("new launch inherited old child: %q", got)
	}
	rec = store.sessions["ao-1"]
	rec.Activity.State = domain.ActivityExited
	store.sessions["ao-1"] = rec
	if err := m.ApplyActivitySignal(context.Background(), "ao-1", ports.ActivitySignal{
		Event: "subagent-stop", SubagentID: "late-child", LaunchID: "launch-2", AgentSessionID: "native-2",
	}); err != nil {
		t.Fatal(err)
	}
	if got := store.sessions["ao-1"].Activity.State; got != domain.ActivityExited {
		t.Fatalf("late child resurrected exited session: %q", got)
	}
}

func TestClaudeStopSnapshotCoversChildWithoutStartHook(t *testing.T) {
	store := newFakeStore()
	store.sessions["ao-1"] = domain.SessionRecord{
		ID: "ao-1", Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: time.Now().UTC()},
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-1"},
	}
	m := New(store, nil)
	running := []string{"child-1"}
	for _, sig := range []ports.ActivitySignal{
		{Valid: true, State: domain.ActivityIdle, Event: "stop", RunningSubagentIDs: &running,
			LaunchID: "launch-1", AgentSessionID: "native-1"},
		{Event: "subagent-stop", SubagentID: "child-1", LaunchID: "launch-1", AgentSessionID: "native-1"},
	} {
		if err := m.ApplyActivitySignal(context.Background(), "ao-1", sig); err != nil {
			t.Fatal(err)
		}
		want := domain.ActivityActive
		if sig.Event == "subagent-stop" {
			want = domain.ActivityIdle
		}
		if got := store.sessions["ao-1"].Activity.State; got != want {
			t.Fatalf("after %s: activity=%q, want %q", sig.Event, got, want)
		}
	}
}
