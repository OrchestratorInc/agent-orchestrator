package sessionmanager

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// TestSend_CoordinationAcceptanceMarker_RealStore is the real-data counterpart
// to TestSend_ConfirmAcceptsCoordinationAcceptanceMarker. Instead of hand-setting
// the checkpoint fields on a fake store, it drives the ACTUAL lifecycle hook
// emission against a real sqlite store, so the test proves the production write
// path yields exactly the fields semanticMessageAccepted reads.
//
// Scenario (the one the reviewer asked for): an ao send (coordination) delivery
// is accepted and its turn finishes between activity polls. The harness emits a
// coordination UserPromptSubmit (advancing the session to active, recording the
// coordination checkpoint keyed to this delivery ID and scoped to the current
// runtime launch) and then a Stop (returning the session to idle). By the time
// send()'s confirmation polls, Active is gone — only the durable marker remains.
// Confirmation must honor it and return nil instead of a false
// ErrDeliveryUnconfirmed that would trigger a duplicate ao send.
func TestSend_CoordinationAcceptanceMarker_RealStore(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	const (
		launch     = "launch-1"
		nativeID   = "native-1"
		deliveryID = "coord-abc123"
	)

	st := sqlitetest.MustOpen(t)
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "coord", Path: t.TempDir(), RegisteredAt: at}); err != nil {
		t.Fatal(err)
	}
	rec, err := st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "coord", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Mode: domain.SessionModeTUI, CreatedAt: at, UpdatedAt: at,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: at},
		Metadata: domain.SessionMetadata{
			RuntimeHandleID: "h1", RuntimeLaunchID: launch,
			AgentSessionID: nativeID, AgentSessionIDLaunchID: launch, NativeIdentityObservedAt: at,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	lcm := lifecycle.New(st, nil)

	// The harness accepts the coordination prompt: a coordination-origin
	// UserPromptSubmit carrying this delivery's CoordinationID. This is what
	// durably records ConversationCheckpointTurnID == deliveryID, scoped to the
	// current launch.
	if err := lcm.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Valid: true, Event: "user-prompt-submit", State: domain.ActivityActive,
		LaunchID: launch, AgentSessionID: nativeID, ProviderTurnID: "turn-1",
		Timestamp:                    at.Add(time.Second),
		ConversationCheckpointOrigin: domain.ConversationCheckpointOriginCoordination,
		CoordinationID:               deliveryID,
	}); err != nil {
		t.Fatal(err)
	}
	// The turn finishes and the session returns to idle before confirmation polls.
	if err := lcm.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Valid: true, Event: "stop", State: domain.ActivityIdle,
		LaunchID: launch, AgentSessionID: nativeID, ProviderTurnID: "turn-1",
		Timestamp:                    at.Add(2 * time.Second),
		ConversationCheckpointOrigin: domain.ConversationCheckpointOriginCoordination,
		CoordinationID:               deliveryID,
	}); err != nil {
		t.Fatal(err)
	}

	// Precondition: the real write path produced exactly the marker
	// semanticMessageAccepted keys on, and the session is idle (Active gone).
	seeded, ok, err := st.GetSession(ctx, rec.ID)
	if err != nil || !ok {
		t.Fatalf("read seeded session: ok=%v err=%v", ok, err)
	}
	if seeded.Activity.State != domain.ActivityIdle {
		t.Fatalf("precondition: Activity.State = %v, want idle", seeded.Activity.State)
	}
	if !semanticMessageAccepted(seeded, deliveryID) {
		t.Fatalf("precondition: coordination marker not recorded by the real lifecycle path: state=%q gen=%q(launch=%q) turnID=%q(want %q)",
			seeded.Metadata.ConversationCheckpointState, seeded.Metadata.ConversationCheckpointGeneration,
			seeded.Metadata.RuntimeLaunchID, seeded.Metadata.ConversationCheckpointTurnID, deliveryID)
	}

	msg := &fakeMessenger{}
	m := New(Deps{
		Store: st, Lifecycle: lcm, Agents: singleAgent{agent: signalingAgent{}},
		Runtime: &fakeRuntime{}, Workspace: &fakeWorkspace{}, Messenger: msg,
		LookPath: func(string) (string, error) { return "/bin/true", nil },
		Logger:   slog.New(slog.DiscardHandler),
	})
	// Shrink the confirmation budget so the loop runs in milliseconds.
	m.sendConfirm = sendConfirmConfig{
		pollInterval:    time.Millisecond,
		attemptDeadline: 2 * time.Millisecond,
		maxAttempts:     3,
	}

	// A coordination send carrying this exact delivery ID. Confirmation must
	// recognize the retained acceptance marker and succeed without nudging.
	if err := m.send(ctx, rec.ID, "status update please", deliveryID, ports.MessageDeliveryOptions{}); err != nil {
		t.Fatalf("send error = %v, want nil (real coordination acceptance marker recognized)", err)
	}
	if got := len(msg.msgs); got != 1 {
		t.Fatalf("messenger calls = %d, want 1 (delivery only; the marker confirms without an Enter nudge)", got)
	}
	final, ok, err := st.GetSession(ctx, rec.ID)
	if err != nil || !ok {
		t.Fatalf("read final session: ok=%v err=%v", ok, err)
	}
	if final.Activity.State == domain.ActivityActive {
		t.Fatalf("Activity.State = active, want it to have stayed idle (evidence path, not the active path)")
	}
}
