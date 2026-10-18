package sessionmanager

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// promptBoundaryStore flips the session flipAfter after its first read: to a
// newer human prompt checkpoint by default, or to an active pane with
// flipActive — standing in for hook events landing while a send's acceptance
// wait is pending. Send is synchronous, so plain map access from GetSession is
// race-free.
type promptBoundaryStore struct {
	*fakeStore
	flipActive bool
	flipAfter  time.Duration
	started    time.Time
	flipped    bool
}

func (s *promptBoundaryStore) GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	if s.started.IsZero() {
		s.started = time.Now()
	}
	if !s.flipped && time.Since(s.started) >= s.flipAfter {
		s.flipped = true
		rec := s.fakeStore.sessions[id]
		if s.flipActive {
			rec.Activity.State = domain.ActivityActive
		} else {
			rec.Metadata.ConversationCheckpointState = domain.ConversationCheckpointPrompt
			rec.Metadata.ConversationCheckpointGeneration = "launch-1"
		}
		s.fakeStore.sessions[id] = rec
	}
	return s.fakeStore.GetSession(ctx, id)
}

// Tests for the session-send acceptance wait (issue #6685): a wrapped
// `ao send` to an idle claude-code TUI used to exit 0 even when the TUI
// swallowed the whole paste, because nothing correlated the wrapped delivery
// id against the agent's prompt-submit hook.

// semanticOnlyAgent advertises semantic acceptance but neither activity
// signal: confirmActive is skipped and only the acceptance wait runs.
type semanticOnlyAgent struct{ fakeAgent }

func (semanticOnlyAgent) EmitsSemanticMessageAcceptance() bool { return true }

// acceptedPastStartup seeds an idle TUI session past the startup input gate,
// running a harness whose adapter claims semantic acceptance.
func acceptedPastStartup(id string) domain.SessionRecord {
	return pastStartupGate(domain.SessionRecord{
		ID: domain.SessionID(id), Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle},
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-1"},
	})
}

// nonEmptyWrites filters Enter-only nudges out of the recorded pane writes.
func nonEmptyWrites(msgs []string) []string {
	kept := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		if strings.TrimSpace(msg) != "" {
			kept = append(kept, msg)
		}
	}
	return kept
}

// acceptSessionSend flips the durable checkpoint to the coordination evidence
// the prompt-submit hook would record for the wrapped delivery id.
func acceptSessionSend(st *fakeStore, id domain.SessionID, message string) {
	if !strings.Contains(message, `<ao-session-delivery id="session-send:`) {
		return
	}
	rec := st.sessions[id]
	rec.Activity.State = domain.ActivityActive
	rec.Metadata.ConversationCheckpointState = domain.ConversationCheckpointCoordination
	rec.Metadata.ConversationCheckpointGeneration = "launch-1"
	rec.Metadata.ConversationCheckpointTurnID = sessionDeliveryIDOf(message)
	st.sessions[id] = rec
}

func sessionDeliveryIDOf(message string) string {
	id, _ := domain.CoordinationDeliveryID(message)
	return id
}

func TestSendIdleTUIAcceptedAfterRePaste(t *testing.T) {
	// Regression for #6685: the first paste is swallowed (the fake TUI accepts
	// nothing), the acceptance wait re-pastes once, and the re-paste IS
	// accepted. The send must succeed and the agent must have received the
	// wrapped message.
	st := newFakeStore()
	st.sessions["s1"] = acceptedPastStartup("s1")
	pastes := 0
	msg := &fakeMessenger{onSend: func(id domain.SessionID, message string) {
		if strings.TrimSpace(message) == "" {
			return
		}
		pastes++
		if pastes >= 2 {
			// Only the re-paste is accepted, like a TUI that dropped the
			// first paste burst at a turn boundary.
			acceptSessionSend(st, id, message)
		}
	}}
	m := newSendTestManager(t, semanticSignalingAgent{}, msg, st)

	if err := m.Send(t.Context(), "s1", "answer: yes, ship it", nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	writes := nonEmptyWrites(msg.msgs)
	if len(writes) != 2 {
		t.Fatalf("pane writes = %d, want the swallowed paste plus one re-paste", len(writes))
	}
	id, ok := domain.CoordinationDeliveryID(writes[0])
	if !ok || !strings.HasPrefix(id, "session-send:") {
		t.Fatalf("first write = %q, want a wrapped session-send delivery", writes[0])
	}
	if writes[0] != writes[1] {
		t.Fatalf("re-paste %q differs from the original paste %q", writes[1], writes[0])
	}
	if st.sessions["s1"].Metadata.ConversationCheckpointTurnID != id {
		t.Fatal("session must end up with the delivery id accepted")
	}
}

func TestSendIdleTUIAcceptedOnFirstPaste(t *testing.T) {
	// Healthy path: the TUI submits the paste, the hook correlates the id, and
	// the send returns with exactly one pane write.
	st := newFakeStore()
	st.sessions["s1"] = acceptedPastStartup("s1")
	msg := &fakeMessenger{onSend: func(id domain.SessionID, message string) {
		acceptSessionSend(st, id, message)
	}}
	m := newSendTestManager(t, semanticSignalingAgent{}, msg, st)

	if err := m.Send(t.Context(), "s1", "hello worker", nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if writes := nonEmptyWrites(msg.msgs); len(writes) != 1 {
		t.Fatalf("pane writes = %d, want 1", len(writes))
	}
}

func TestSendIdleTUIUnacceptedFailsAfterRePaste(t *testing.T) {
	// The TUI swallows every paste and no hook ever fires: the send must fail
	// with ErrSendDeliveryUnconfirmed instead of exiting 0 (issue #6685), after
	// exactly the bounded number of pane writes.
	st := newFakeStore()
	st.sessions["s1"] = acceptedPastStartup("s1")
	msg := &fakeMessenger{}
	m := newSendTestManager(t, semanticSignalingAgent{}, msg, st)

	err := m.Send(t.Context(), "s1", "anyone home?", nil)
	if !errors.Is(err, ErrSendDeliveryUnconfirmed) {
		t.Fatalf("Send error = %v, want ErrSendDeliveryUnconfirmed", err)
	}
	if writes := nonEmptyWrites(msg.msgs); len(writes) != m.sendAcceptance.maxPastes {
		t.Fatalf("pane writes = %d, want %d", len(writes), m.sendAcceptance.maxPastes)
	}
}

func TestSendIdleTUINeverRepastesAfterHumanPrompt(t *testing.T) {
	// A human prompt boundary lands on the pane while the acceptance wait is
	// pending: the message is superseded, no re-paste may fire, and the send
	// must not fail.
	base := newFakeStore()
	base.sessions["s1"] = acceptedPastStartup("s1")
	// semanticOnlyAgent skips confirmActive entirely (no submit/blocked
	// signals), so the wait starts within milliseconds and the 2ms flip lands
	// deterministically inside the 4ms evidence window — no wall-clock race
	// with the Enter budget (review finding).
	st := &promptBoundaryStore{fakeStore: base, flipAfter: 2 * time.Millisecond}
	msg := &fakeMessenger{}
	m := newSendTestManager(t, semanticOnlyAgent{}, msg, st.fakeStore)
	// Re-wire the manager onto the wrapper so the acceptance wait observes the
	// mid-wait boundary flip.
	m.store = st

	if err := m.Send(t.Context(), "s1", "might be swallowed", nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if writes := nonEmptyWrites(msg.msgs); len(writes) != 1 {
		t.Fatalf("pane writes = %d, want 1 (no re-paste after a human prompt)", len(writes))
	}
}

func TestSendActivePaneEndsWaitWithoutRepaste(t *testing.T) {
	// Review finding: a busy pane is the visible delivery state. A send into a
	// session that turns active mid-window must end the wait without a
	// re-paste and without a false unconfirmed error — a claude-code worker
	// mid-turn longer than one evidence window would otherwise get duplicate
	// pastes into a busy composer.
	base := newFakeStore()
	base.sessions["s1"] = acceptedPastStartup("s1")
	// The pane flips to active ~500us in (well inside the 4ms window) and no
	// hook evidence ever arrives.
	st := &promptBoundaryStore{fakeStore: base, flipActive: true, flipAfter: 500 * time.Microsecond}
	msg := &fakeMessenger{}
	m := newSendTestManager(t, semanticOnlyAgent{}, msg, st.fakeStore)
	m.store = st

	if err := m.Send(t.Context(), "s1", "mid-turn instruction", nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if writes := nonEmptyWrites(msg.msgs); len(writes) != 1 {
		t.Fatalf("pane writes = %d, want 1 (no re-paste into a busy pane)", len(writes))
	}
}

func TestSendWithoutAcceptanceSignalKeepsPaneWriteSemantics(t *testing.T) {
	// A harness that cannot correlate acceptance keeps today's behavior: one
	// pane write, success, no wait, no re-paste.
	st := newFakeStore()
	st.sessions["s1"] = acceptedPastStartup("s1")
	msg := &fakeMessenger{}
	m := newSendTestManager(t, fakeAgent{}, msg, st)

	if err := m.Send(t.Context(), "s1", "plain harness", nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if writes := nonEmptyWrites(msg.msgs); len(writes) != 1 {
		t.Fatalf("pane writes = %d, want 1", len(writes))
	}
}
