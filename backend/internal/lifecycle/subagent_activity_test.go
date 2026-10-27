package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestCodexSubagentActivityKeepsSessionWorkingUntilLastChildStops(t *testing.T) {
	store := newFakeStore()
	start := time.Date(2026, 10, 4, 11, 15, 40, 0, time.UTC)
	store.sessions["ao-1"] = domain.SessionRecord{
		ID: "ao-1", Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: start},
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-1"},
	}
	m := New(store, nil)
	apply := func(second int, event, child string, state domain.ActivityState, want domain.ActivityState) {
		t.Helper()
		if err := m.ApplyActivitySignal(context.Background(), "ao-1", ports.ActivitySignal{
			Valid: state != "", State: state, Event: event, SubagentID: child,
			LaunchID: "launch-1", AgentSessionID: "native-root",
			Timestamp: start.Add(time.Duration(second) * time.Second),
		}); err != nil {
			t.Fatalf("%s: %v", event, err)
		}
		if got := store.sessions["ao-1"].Activity.State; got != want {
			t.Fatalf("after %s for %q: activity=%q, want %q", event, child, got, want)
		}
	}
	apply(1, "user-prompt-submit", "", domain.ActivityActive, domain.ActivityActive)
	apply(2, "subagent-start", "child-1", "", domain.ActivityActive)
	apply(3, "subagent-start", "child-2", "", domain.ActivityActive)
	apply(4, "stop", "", domain.ActivityIdle, domain.ActivityActive)
	apply(5, "terminal-idle", "", domain.ActivityIdle, domain.ActivityActive)
	m = New(store, nil)
	apply(6, "subagent-stop", "child-1", "", domain.ActivityActive)
	apply(7, "subagent-stop", "child-2", "", domain.ActivityIdle)
	apply(8, "user-prompt-submit", "child-2", domain.ActivityActive, domain.ActivityActive)
	apply(9, "subagent-stop", "child-2", "", domain.ActivityIdle)
	apply(3, "subagent-start", "child-2", "", domain.ActivityIdle)
	if got := store.sessions["ao-1"].Metadata.AgentSessionID; got != "native-root" {
		t.Fatalf("child hook changed root session ID to %q", got)
	}
}

func TestCodexSpawnToolResultBridgesDelayedChildStart(t *testing.T) {
	store := newFakeStore()
	start := time.Date(2026, 10, 4, 13, 1, 0, 0, time.UTC)
	store.sessions["ao-1"] = domain.SessionRecord{
		ID: "ao-1", Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: start},
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-1"},
	}
	m := New(store, nil)
	apply := func(second int, event, id string, state, want domain.ActivityState) {
		t.Helper()
		if err := m.ApplyActivitySignal(context.Background(), "ao-1", ports.ActivitySignal{
			Valid: state != "", State: state, Event: event, SubagentID: id,
			LaunchID: "launch-1", AgentSessionID: "native-root",
			Timestamp: start.Add(time.Duration(second) * time.Second),
		}); err != nil {
			t.Fatalf("%s: %v", event, err)
		}
		if got := store.sessions["ao-1"].Activity.State; got != want {
			t.Fatalf("after %s for %q: activity=%q, want %q", event, id, got, want)
		}
	}
	apply(1, "user-prompt-submit", "", domain.ActivityActive, domain.ActivityActive)
	apply(2, "subagent-spawn", "call-1", "", domain.ActivityActive)
	apply(3, "subagent-spawn", "call-2", "", domain.ActivityActive)
	apply(4, "stop", "", domain.ActivityIdle, domain.ActivityActive)
	apply(5, "terminal-idle", "", domain.ActivityIdle, domain.ActivityActive)
	m = New(store, nil)
	apply(6, "subagent-start", "child-1", "", domain.ActivityActive)
	apply(7, "subagent-stop", "child-1", "", domain.ActivityActive)
	apply(8, "subagent-start", "child-2", "", domain.ActivityActive)
	apply(9, "subagent-stop", "child-2", "", domain.ActivityIdle)
	// A child stop still clears the provisional spawn if its start hook was lost.
	apply(10, "user-prompt-submit", "", domain.ActivityActive, domain.ActivityActive)
	apply(11, "subagent-spawn", "call-3", "", domain.ActivityActive)
	apply(12, "stop", "", domain.ActivityIdle, domain.ActivityActive)
	apply(13, "subagent-stop", "child-3", "", domain.ActivityIdle)
	var facts subagentActivityFacts
	if err := json.Unmarshal([]byte(store.sessions["ao-1"].Metadata.CodexActivityFacts), &facts); err != nil {
		t.Fatal(err)
	}
	if len(facts.PendingSpawns) != 0 {
		t.Fatalf("unreconciled spawns: %+v", facts.PendingSpawns)
	}
}

func TestCodexSubagentActivityResetsOnNewLaunch(t *testing.T) {
	store := newFakeStore()
	store.sessions["ao-1"] = domain.SessionRecord{
		ID: "ao-1", Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityActive},
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-1"},
	}
	m := New(store, nil)
	if err := m.ApplyActivitySignal(context.Background(), "ao-1", ports.ActivitySignal{
		Event: "subagent-start", SubagentID: "old-child", LaunchID: "launch-1",
	}); err != nil {
		t.Fatal(err)
	}
	rec := store.sessions["ao-1"]
	rec.Metadata.RuntimeLaunchID = "launch-2"
	store.sessions["ao-1"] = rec
	if err := m.ApplyActivitySignal(context.Background(), "ao-1", ports.ActivitySignal{
		Valid: true, State: domain.ActivityIdle, Event: "stop", LaunchID: "launch-2",
	}); err != nil {
		t.Fatal(err)
	}
	if got := store.sessions["ao-1"].Activity.State; got != domain.ActivityIdle {
		t.Fatalf("new launch inherited old child: %q", got)
	}
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
	// Claude's permission notification has no child id. It must not turn the
	// parent into a blocked turn after the child's correlated tool completes.
	apply(7, "notification", "", domain.ActivityBlocked, "", nil, domain.ActivityBlocked)
	apply(8, "post-tool-use", "child-1", domain.ActivityActive, "tool-1", nil, domain.ActivityActive)
	apply(9, "subagent-stop", "child-2", "", "", nil, domain.ActivityActive)
	apply(10, "subagent-stop", "child-1", "", "", nil, domain.ActivityIdle)
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

func TestClaudeActivityFactsPruneStoppedChildrenAfterSnapshot(t *testing.T) {
	store := newFakeStore()
	start := time.Date(2026, 10, 3, 18, 4, 40, 0, time.UTC)
	store.sessions["ao-1"] = domain.SessionRecord{
		ID: "ao-1", Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: start},
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-1"},
	}
	m := New(store, nil)
	for i := range 20 {
		id := fmt.Sprintf("child-%d", i)
		for _, sig := range []ports.ActivitySignal{
			{Event: "subagent-start", SubagentID: id, Timestamp: start.Add(time.Duration(2*i+1) * time.Second)},
			{Event: "subagent-stop", SubagentID: id, Timestamp: start.Add(time.Duration(2*i+2) * time.Second)},
		} {
			sig.LaunchID = "launch-1"
			if err := m.ApplyActivitySignal(context.Background(), "ao-1", sig); err != nil {
				t.Fatal(err)
			}
		}
	}
	empty := []string{}
	if err := m.ApplyActivitySignal(context.Background(), "ao-1", ports.ActivitySignal{
		Valid: true, State: domain.ActivityIdle, Event: "stop", RunningSubagentIDs: &empty,
		LaunchID: "launch-1", Timestamp: start.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	var facts subagentActivityFacts
	if err := json.Unmarshal([]byte(store.sessions["ao-1"].Metadata.ClaudeActivityFacts), &facts); err != nil {
		t.Fatal(err)
	}
	if len(facts.Children) != 0 {
		t.Fatalf("empty snapshot retained %d stopped children", len(facts.Children))
	}
}

func TestClaudeDelayedStartAfterSnapshotIsSuppressed(t *testing.T) {
	start := time.Date(2026, 10, 3, 18, 4, 40, 0, time.UTC)
	encoded, err := json.Marshal(subagentActivityFacts{
		LaunchID: "launch-1", ParentState: domain.ActivityIdle,
		SnapshotAt: start.Add(20 * time.Second).UnixNano(),
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := domain.SessionRecord{
		Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle},
		Metadata: domain.SessionMetadata{ClaudeActivityFacts: string(encoded)},
	}
	for _, event := range []string{"subagent-start", "subagent-stop"} {
		sig, facts, err := reduceSubagentActivity(rec, ports.ActivitySignal{
			Event: event, SubagentID: "late-child", LaunchID: "launch-1",
			Timestamp: start.Add(10 * time.Second),
		}, start.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if sig.Valid || facts != rec.Metadata.ClaudeActivityFacts {
			t.Fatalf("delayed %s was not suppressed: signal=%+v facts=%s", event, sig, facts)
		}
	}
}

func TestClaudeCorruptActivityFactsRecoverOnNextStop(t *testing.T) {
	rec := domain.SessionRecord{
		Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityActive},
		Metadata: domain.SessionMetadata{ClaudeActivityFacts: "{broken"},
	}
	empty := []string{}
	sig, facts, err := reduceSubagentActivity(rec, ports.ActivitySignal{
		Valid: true, State: domain.ActivityIdle, Event: "stop", LaunchID: "launch-1",
		RunningSubagentIDs: &empty,
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if !sig.Valid || sig.State != domain.ActivityIdle || !json.Valid([]byte(facts)) {
		t.Fatalf("corrupt facts did not recover: signal=%+v facts=%s", sig, facts)
	}
}

func TestClaudeTerminalIdleDoesNotRewriteRunningChild(t *testing.T) {
	store := newFakeStore()
	start := time.Date(2026, 10, 3, 18, 4, 40, 0, time.UTC)
	store.sessions["ao-1"] = domain.SessionRecord{
		ID: "ao-1", Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: start},
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-1"},
	}
	m := New(store, nil)
	for _, sig := range []ports.ActivitySignal{
		{Event: "subagent-start", SubagentID: "child-1", Timestamp: start.Add(time.Second)},
		{Valid: true, State: domain.ActivityIdle, Event: "stop", Timestamp: start.Add(2 * time.Second),
			RunningSubagentIDs: &[]string{"child-1"}},
	} {
		sig.LaunchID = "launch-1"
		if err := m.ApplyActivitySignal(context.Background(), "ao-1", sig); err != nil {
			t.Fatal(err)
		}
	}
	before := store.sessions["ao-1"]
	if err := m.ApplyActivitySignal(context.Background(), "ao-1", ports.ActivitySignal{
		Valid: true, State: domain.ActivityIdle, Event: "terminal-idle",
		LaunchID: "launch-1", Timestamp: start.Add(3 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	after := store.sessions["ao-1"]
	if after.Revision != before.Revision || after.Activity != before.Activity ||
		after.Metadata.ClaudeActivityFacts != before.Metadata.ClaudeActivityFacts {
		t.Fatalf("terminal idle rewrote a running child: before=%+v after=%+v", before, after)
	}
}

func TestCodexTerminalIdleRecoversOrphanedChildAfterFactTTL(t *testing.T) {
	store := newFakeStore()
	start := time.Date(2026, 10, 4, 18, 4, 40, 0, time.UTC)
	store.sessions["ao-1"] = domain.SessionRecord{
		ID: "ao-1", Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: start},
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-1"},
	}
	m := New(store, nil)
	for _, sig := range []ports.ActivitySignal{
		{Event: "subagent-start", SubagentID: "child-1", Timestamp: start.Add(time.Second)},
		{Valid: true, State: domain.ActivityIdle, Event: "stop", Timestamp: start.Add(2 * time.Second)},
	} {
		sig.LaunchID = "launch-1"
		if err := m.ApplyActivitySignal(context.Background(), "ao-1", sig); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.ApplyActivitySignal(context.Background(), "ao-1", ports.ActivitySignal{
		Valid: true, State: domain.ActivityIdle, Event: "terminal-idle", LaunchID: "launch-1",
		Timestamp: start.Add(subagentFactTTL + time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if got := store.sessions["ao-1"].Activity.State; got != domain.ActivityIdle {
		t.Fatalf("orphaned child kept session %q, want idle", got)
	}
	var facts subagentActivityFacts
	if err := json.Unmarshal([]byte(store.sessions["ao-1"].Metadata.CodexActivityFacts), &facts); err != nil {
		t.Fatal(err)
	}
	if child := facts.Children["child-1"]; child.Running {
		t.Fatalf("expired child remained running: %+v", child)
	}
}

func TestCodexTerminalIdleExpiresUnmatchedSpawnAfterFactTTL(t *testing.T) {
	store := newFakeStore()
	start := time.Date(2026, 10, 4, 18, 14, 40, 0, time.UTC)
	store.sessions["ao-1"] = domain.SessionRecord{
		ID: "ao-1", Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: start},
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-1"},
	}
	m := New(store, nil)
	for _, sig := range []ports.ActivitySignal{
		{Event: "subagent-spawn", SubagentID: "call-1", Timestamp: start.Add(time.Second)},
		{Valid: true, State: domain.ActivityIdle, Event: "stop", Timestamp: start.Add(2 * time.Second)},
	} {
		sig.LaunchID = "launch-1"
		if err := m.ApplyActivitySignal(context.Background(), "ao-1", sig); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.ApplyActivitySignal(context.Background(), "ao-1", ports.ActivitySignal{
		Valid: true, State: domain.ActivityIdle, Event: "terminal-idle", LaunchID: "launch-1",
		Timestamp: start.Add(subagentFactTTL + time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if got := store.sessions["ao-1"].Activity.State; got != domain.ActivityIdle {
		t.Fatalf("unmatched spawn kept session %q, want idle", got)
	}
	var facts subagentActivityFacts
	if err := json.Unmarshal([]byte(store.sessions["ao-1"].Metadata.CodexActivityFacts), &facts); err != nil {
		t.Fatal(err)
	}
	if len(facts.PendingSpawns) != 0 {
		t.Fatalf("expired pending spawns remained: %+v", facts.PendingSpawns)
	}
}

func TestCodexActivityFactsPruneOldTombstones(t *testing.T) {
	store := newFakeStore()
	start := time.Date(2026, 10, 4, 19, 4, 40, 0, time.UTC)
	store.sessions["ao-1"] = domain.SessionRecord{
		ID: "ao-1", Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: start},
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-1"},
	}
	m := New(store, nil)
	for i := range maxSubagentChildTombstones + 20 {
		at := start.Add(time.Duration(i) * time.Second)
		for _, sig := range []ports.ActivitySignal{
			{Event: "subagent-start", SubagentID: fmt.Sprintf("child-%d", i), Timestamp: at},
			{Event: "subagent-stop", SubagentID: fmt.Sprintf("child-%d", i), Timestamp: at.Add(time.Millisecond)},
		} {
			sig.LaunchID = "launch-1"
			if err := m.ApplyActivitySignal(context.Background(), "ao-1", sig); err != nil {
				t.Fatal(err)
			}
		}
	}
	var facts subagentActivityFacts
	if err := json.Unmarshal([]byte(store.sessions["ao-1"].Metadata.CodexActivityFacts), &facts); err != nil {
		t.Fatal(err)
	}
	if len(facts.Children) > maxSubagentChildTombstones {
		t.Fatalf("retained %d child tombstones, want at most %d", len(facts.Children), maxSubagentChildTombstones)
	}
}

func TestClaudeStaleNotificationAfterChildStopDoesNotReblockParent(t *testing.T) {
	store := newFakeStore()
	start := time.Date(2026, 10, 3, 20, 4, 40, 0, time.UTC)
	store.sessions["ao-1"] = domain.SessionRecord{
		ID: "ao-1", Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: start},
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-1"},
	}
	m := New(store, nil)
	apply := func(offset time.Duration, event, child string, state domain.ActivityState, running *[]string, want domain.ActivityState) {
		t.Helper()
		sig := ports.ActivitySignal{
			Valid: state != "", State: state, Event: event, SubagentID: child,
			RunningSubagentIDs: running, LaunchID: "launch-1", AgentSessionID: "native-1",
			Timestamp: start.Add(offset),
		}
		if event == "pre-tool-use" || event == "post-tool-use" || event == "permission-request" {
			sig.ToolName = "Bash"
			sig.ToolUseID = "tool-1"
		}
		if err := m.ApplyActivitySignal(context.Background(), "ao-1", sig); err != nil {
			t.Fatal(err)
		}
		if got := store.sessions["ao-1"].Activity.State; got != want {
			t.Fatalf("after %s: activity=%q, want %q", event, got, want)
		}
	}
	apply(time.Second, "user-prompt-submit", "", domain.ActivityActive, nil, domain.ActivityActive)
	apply(2*time.Second, "subagent-start", "child-1", "", nil, domain.ActivityActive)
	running := []string{"child-1"}
	apply(3*time.Second, "stop", "", domain.ActivityIdle, &running, domain.ActivityActive)
	apply(4*time.Second, "pre-tool-use", "child-1", domain.ActivityActive, nil, domain.ActivityActive)
	apply(4*time.Second, "permission-request", "child-1", domain.ActivityBlocked, nil, domain.ActivityBlocked)
	apply(5*time.Second, "post-tool-use", "child-1", domain.ActivityActive, nil, domain.ActivityActive)
	apply(6*time.Second, "subagent-stop", "child-1", "", nil, domain.ActivityIdle)
	// This notification arrived after the child stopped. It has no identity, so
	// its blocked state cannot be allowed to overwrite the completed child turn.
	apply(7*time.Second, "notification", "", domain.ActivityBlocked, nil, domain.ActivityIdle)
	apply(8*time.Second, "user-prompt-submit", "", domain.ActivityActive, nil, domain.ActivityActive)
	apply(9*time.Second, "notification", "", domain.ActivityBlocked, nil, domain.ActivityBlocked)
}

func TestClaudeSubagentStopSnapshotClearsSiblingWithLostStopHook(t *testing.T) {
	store := newFakeStore()
	start := time.Date(2026, 10, 3, 18, 4, 40, 0, time.UTC)
	store.sessions["ao-1"] = domain.SessionRecord{
		ID: "ao-1", Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: start},
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-1"},
	}
	m := New(store, nil)
	for _, sig := range []ports.ActivitySignal{
		{Event: "subagent-start", SubagentID: "child-1", Timestamp: start.Add(time.Second)},
		{Event: "subagent-start", SubagentID: "child-2", Timestamp: start.Add(2 * time.Second)},
		{Valid: true, State: domain.ActivityIdle, Event: "stop", Timestamp: start.Add(3 * time.Second),
			RunningSubagentIDs: &[]string{"child-1", "child-2"}},
		// child-1's stop hook is lost. Claude's parent-scoped snapshot on
		// child-2's stop proves neither child remains in flight.
		{Event: "subagent-stop", SubagentID: "child-2", Timestamp: start.Add(4 * time.Second),
			RunningSubagentIDs: &[]string{}},
	} {
		sig.LaunchID = "launch-1"
		if err := m.ApplyActivitySignal(context.Background(), "ao-1", sig); err != nil {
			t.Fatal(err)
		}
	}
	if got := store.sessions["ao-1"].Activity.State; got != domain.ActivityIdle {
		t.Fatalf("empty SubagentStop snapshot left a lost sibling active: %q", got)
	}
}

func TestClaudeDuplicateSubagentStopStillAppliesNewerSnapshot(t *testing.T) {
	store := newFakeStore()
	start := time.Date(2026, 10, 3, 18, 4, 40, 0, time.UTC)
	store.sessions["ao-1"] = domain.SessionRecord{
		ID: "ao-1", Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: start},
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-1"},
	}
	m := New(store, nil)
	for _, sig := range []ports.ActivitySignal{
		{Event: "subagent-start", SubagentID: "child-1", Timestamp: start.Add(time.Second)},
		{Event: "subagent-start", SubagentID: "child-2", Timestamp: start.Add(2 * time.Second)},
		{Valid: true, State: domain.ActivityIdle, Event: "stop", Timestamp: start.Add(3 * time.Second),
			RunningSubagentIDs: &[]string{"child-1", "child-2"}},
		{Event: "subagent-stop", SubagentID: "child-2", Timestamp: start.Add(4 * time.Second)},
		// This duplicate stop carries a newer snapshot. Child-1's stop hook
		// was lost, and the registry now proves it is no longer running.
		{Event: "subagent-stop", SubagentID: "child-2", Timestamp: start.Add(5 * time.Second),
			RunningSubagentIDs: &[]string{}},
	} {
		sig.LaunchID = "launch-1"
		if err := m.ApplyActivitySignal(context.Background(), "ao-1", sig); err != nil {
			t.Fatal(err)
		}
	}
	if got := store.sessions["ao-1"].Activity.State; got != domain.ActivityIdle {
		t.Fatalf("newer snapshot on duplicate stop left sibling active: %q", got)
	}
}

func TestClaudeStopWithoutSnapshotDoesNotProveChildFinished(t *testing.T) {
	store := newFakeStore()
	start := time.Date(2026, 10, 3, 18, 4, 40, 0, time.UTC)
	store.sessions["ao-1"] = domain.SessionRecord{
		ID: "ao-1", Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: start},
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-1"},
	}
	m := New(store, nil)
	for _, sig := range []ports.ActivitySignal{
		{Event: "subagent-start", SubagentID: "child-1", Timestamp: start.Add(time.Second)},
		{Valid: true, State: domain.ActivityIdle, Event: "stop", Timestamp: start.Add(2 * time.Second)},
	} {
		sig.LaunchID = "launch-1"
		if err := m.ApplyActivitySignal(context.Background(), "ao-1", sig); err != nil {
			t.Fatal(err)
		}
	}
	if got := store.sessions["ao-1"].Activity.State; got != domain.ActivityActive {
		t.Fatalf("missing task registry falsely ended child: %q", got)
	}
}
