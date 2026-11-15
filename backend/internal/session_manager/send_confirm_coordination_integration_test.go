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

	if err := lcm.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Valid: true, Event: "user-prompt-submit", State: domain.ActivityActive,
		LaunchID: launch, AgentSessionID: nativeID, ProviderTurnID: "turn-1",
		Timestamp:                    at.Add(time.Second),
		ConversationCheckpointOrigin: domain.ConversationCheckpointOriginCoordination,
		CoordinationID:               deliveryID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := lcm.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Valid: true, Event: "stop", State: domain.ActivityIdle,
		LaunchID: launch, AgentSessionID: nativeID, ProviderTurnID: "turn-1",
		Timestamp:                    at.Add(2 * time.Second),
		ConversationCheckpointOrigin: domain.ConversationCheckpointOriginCoordination,
		CoordinationID:               deliveryID,
	}); err != nil {
		t.Fatal(err)
	}

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
	m.sendConfirm = sendConfirmConfig{
		pollInterval:    time.Millisecond,
		attemptDeadline: 2 * time.Millisecond,
		maxAttempts:     3,
	}

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
