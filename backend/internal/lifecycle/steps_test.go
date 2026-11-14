package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestStepsPairPreWithPostByToolUseID(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = working("mer-1")
	ctx := context.Background()
	pre := func(id, tool string) {
		t.Helper()
		if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityActive, Event: "pre-tool-use", ToolName: tool, ToolUseID: id}); err != nil {
			t.Fatal(err)
		}
	}
	post := func(id, tool, event string) {
		t.Helper()
		if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityActive, Event: event, ToolName: tool, ToolUseID: id}); err != nil {
			t.Fatal(err)
		}
	}
	pre("t1", "Bash")
	pre("t2", "Read")
	post("t2", "Read", "post-tool-use")
	post("t1", "Bash", "post-tool-use-failure")
	post("t9", "Bash", "post-tool-use") // unmatched post: ignored

	steps := m.Steps("mer-1")
	if len(steps) != 2 {
		t.Fatalf("steps = %+v", steps)
	}
	if steps[0].Tool != "Bash" || steps[0].EndedAt.IsZero() || !steps[0].Failed {
		t.Fatalf("first step = %+v", steps[0])
	}
	if steps[1].Tool != "Read" || steps[1].EndedAt.IsZero() || steps[1].Failed {
		t.Fatalf("second step = %+v", steps[1])
	}
	if m.Steps("other") != nil && len(m.Steps("other")) != 0 {
		t.Fatal("unknown session must have no steps")
	}
}

func TestStepsRingKeepsOnlyTheNewest(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = working("mer-1")
	ctx := context.Background()
	for i := 0; i < stepRingSize+5; i++ {
		if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{
			Valid: true, State: domain.ActivityActive, Event: "pre-tool-use", ToolName: "Read",
			ToolUseID: fmt.Sprintf("t%d", i), Timestamp: time.Unix(int64(i), 0),
		}); err != nil {
			t.Fatal(err)
		}
	}
	steps := m.Steps("mer-1")
	if len(steps) != stepRingSize || steps[0].ToolUseID != "t5" {
		t.Fatalf("ring = %d entries, first %q", len(steps), steps[0].ToolUseID)
	}
}

func TestStepsClearedWhenRuntimeEnds(t *testing.T) {
	ctx := context.Background()
	openStep := func(t *testing.T, m *Manager) {
		t.Helper()
		if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityActive, Event: "pre-tool-use", ToolName: "Bash", ToolUseID: "t1"}); err != nil {
			t.Fatal(err)
		}
		if len(m.Steps("mer-1")) != 1 {
			t.Fatal("pre-tool-use did not record a step")
		}
	}

	t.Run("terminated", func(t *testing.T) {
		m, st, _ := newManager()
		st.sessions["mer-1"] = working("mer-1")
		openStep(t, m)
		if err := m.MarkTerminated(ctx, "mer-1"); err != nil {
			t.Fatal(err)
		}
		if steps := m.Steps("mer-1"); len(steps) != 0 {
			t.Fatalf("terminated session kept steps %+v", steps)
		}
	})

	t.Run("controller switch", func(t *testing.T) {
		m, st, _ := newManager()
		rec := working("mer-1")
		rec.Mode = domain.SessionModeTUI
		rec.Metadata.AgentSessionID = "native-1"
		st.sessions["mer-1"] = rec
		openStep(t, m)
		if changed, err := m.CommitControllerEpoch(ctx, "mer-1", domain.SessionModeTUI, domain.SessionModeChat, "native-1", false); err != nil || !changed {
			t.Fatalf("CommitControllerEpoch: changed=%v err=%v", changed, err)
		}
		if steps := m.Steps("mer-1"); len(steps) != 0 {
			t.Fatalf("old runtime's open step survived the switch: %+v", steps)
		}
	})
}

// TestStepsOnlyLandForAcceptedSignals: a tool step is recorded only once every
// fence has accepted the signal and its projection is applied. A rejected or
// failed signal must not leave a phantom current or recent step behind.
func TestStepsOnlyLandForAcceptedSignals(t *testing.T) {
	ctx := context.Background()
	pre := ports.ActivitySignal{Valid: true, State: domain.ActivityActive, Event: "pre-tool-use", ToolName: "Bash", ToolUseID: "t1"}
	// Idle, so the active pre-tool-use is a state change the store must write;
	// an active row would skip the write and never reach the failure paths.
	idle := func() domain.SessionRecord {
		rec := working("mer-1")
		rec.Activity.State = domain.ActivityIdle
		return rec
	}

	t.Run("exited workload", func(t *testing.T) {
		m, st, _ := newManager()
		rec := working("mer-1")
		rec.Activity.State = domain.ActivityExited
		st.sessions["mer-1"] = rec
		if err := m.ApplyActivitySignal(ctx, "mer-1", pre); err != nil {
			t.Fatal(err)
		}
		if steps := m.Steps("mer-1"); len(steps) != 0 {
			t.Fatalf("delayed tool hook after exit recorded a step: %+v", steps)
		}
	})

	t.Run("stale native identity", func(t *testing.T) {
		m, st, _ := newManager()
		observed := time.Unix(2000, 0)
		rec := working("mer-1")
		rec.Metadata.AgentSessionID = "native-new"
		rec.Metadata.NativeIdentityObservedAt = observed
		st.sessions["mer-1"] = rec
		stale := pre
		stale.AgentSessionID = "native-old"
		stale.Timestamp = observed.Add(-time.Second)
		if err := m.ApplyActivitySignal(ctx, "mer-1", stale); err != nil {
			t.Fatal(err)
		}
		if steps := m.Steps("mer-1"); len(steps) != 0 {
			t.Fatalf("signal from a replaced native session recorded a step: %+v", steps)
		}
	})

	t.Run("failed write", func(t *testing.T) {
		st := &promptConflictStore{fakeStore: newFakeStore(), writeErr: errors.New("write failed")}
		st.sessions["mer-1"] = idle()
		m := New(st, &fakeMessenger{})
		if err := m.ApplyActivitySignal(ctx, "mer-1", pre); err == nil {
			t.Fatal("want the write error")
		}
		if steps := m.Steps("mer-1"); len(steps) != 0 {
			t.Fatalf("failed projection left a step: %+v", steps)
		}
	})

	t.Run("exhausted revision retries", func(t *testing.T) {
		st := &promptConflictStore{fakeStore: newFakeStore(), alwaysConflict: true}
		st.sessions["mer-1"] = idle()
		m := New(st, &fakeMessenger{})
		if err := m.ApplyActivitySignal(ctx, "mer-1", pre); err == nil {
			t.Fatal("want contention error")
		}
		if steps := m.Steps("mer-1"); len(steps) != 0 {
			t.Fatalf("abandoned projection left a step: %+v", steps)
		}
	})

	t.Run("one retry records once", func(t *testing.T) {
		st := &promptConflictStore{fakeStore: newFakeStore(), conflict: true}
		st.sessions["mer-1"] = idle()
		m := New(st, &fakeMessenger{})
		if err := m.ApplyActivitySignal(ctx, "mer-1", pre); err != nil {
			t.Fatal(err)
		}
		if steps := m.Steps("mer-1"); len(steps) != 1 || steps[0].ToolUseID != "t1" {
			t.Fatalf("retried projection steps = %+v, want exactly one", steps)
		}
	})
}

// blockedOnDialog drives a session into blocked through the real signal path so
// later tool events are suppressed for the state while still really having run.
func blockedOnDialog(t *testing.T, m *Manager, st *fakeStore, id domain.SessionID, toolName, toolUseID string) {
	t.Helper()
	ctx := context.Background()
	apply := func(s ports.ActivitySignal) {
		t.Helper()
		if err := m.ApplyActivitySignal(ctx, id, s); err != nil {
			t.Fatal(err)
		}
	}
	apply(ports.ActivitySignal{Valid: true, State: domain.ActivityActive, Event: "pre-tool-use", ToolName: toolName, ToolUseID: toolUseID})
	apply(ports.ActivitySignal{Valid: true, State: domain.ActivityBlocked, Event: "permission-request", ToolName: toolName})
	if got := st.sessions[id].Activity.State; got != domain.ActivityBlocked {
		t.Fatalf("setup: state = %q, want blocked", got)
	}
}

func TestStepsRecordSuppressedToolEventsWhileBlocked(t *testing.T) {
	// A sibling tool runs while a dialog is on screen: the session must stay
	// blocked, but the tool really ran, so its step must open and close — state
	// suppression must not swallow step tracking.
	m, st, _ := newManager()
	st.sessions["mer-1"] = working("mer-1")
	blockedOnDialog(t, m, st, "mer-1", "Bash", "toolu_1")

	ctx := context.Background()
	sibling := func(event string) {
		t.Helper()
		if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityActive, Event: event, ToolName: "Read", ToolUseID: "toolu_2"}); err != nil {
			t.Fatal(err)
		}
		if got := st.sessions["mer-1"].Activity.State; got != domain.ActivityBlocked {
			t.Fatalf("state after %s = %q, want blocked", event, got)
		}
	}
	sibling("pre-tool-use")
	sibling("post-tool-use")

	var step *domain.SessionStep
	for _, s := range m.Steps("mer-1") {
		if s.ToolUseID == "toolu_2" {
			step = &s
			break
		}
	}
	if step == nil {
		t.Fatalf("sibling step missing from %+v", m.Steps("mer-1"))
	}
	if step.EndedAt.IsZero() {
		t.Fatalf("sibling step = %+v, want closed", *step)
	}
	if step.Failed {
		t.Fatalf("sibling step = %+v, want a success", *step)
	}
}

func TestStepsSweptFailureClosesStepWhileBlocked(t *testing.T) {
	// The plugin sweep closes a turn's leftover tool as failed. While the session
	// is blocked on an unrelated dialog that post is suppressed for the state,
	// but the step must still be marked failed and closed — otherwise the exact
	// "running forever" step this PR fixes reappears behind a dialog.
	m, st, _ := newManager()
	st.sessions["mer-1"] = working("mer-1")
	blockedOnDialog(t, m, st, "mer-1", "Bash", "toolu_1")

	ctx := context.Background()
	for _, s := range []ports.ActivitySignal{
		{Valid: true, State: domain.ActivityActive, Event: "pre-tool-use", ToolName: "Read", ToolUseID: "toolu_left"},
		{Valid: true, State: domain.ActivityActive, Event: "post-tool-use-failure", ToolName: "Read", ToolUseID: "toolu_left"},
	} {
		if err := m.ApplyActivitySignal(ctx, "mer-1", s); err != nil {
			t.Fatal(err)
		}
	}

	var step *domain.SessionStep
	for _, s := range m.Steps("mer-1") {
		if s.ToolUseID == "toolu_left" {
			step = &s
			break
		}
	}
	if step == nil {
		t.Fatalf("swept step missing from %+v", m.Steps("mer-1"))
	}
	if step.EndedAt.IsZero() || !step.Failed {
		t.Fatalf("swept step = %+v, want closed and failed", *step)
	}
	if got := st.sessions["mer-1"].Activity.State; got != domain.ActivityBlocked {
		t.Fatalf("state after the swept failure = %q, want blocked", got)
	}
}
