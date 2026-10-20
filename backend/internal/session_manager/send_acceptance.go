package sessionmanager

import (
	"context"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
)

// deliveryCheckpointIdentity is the durable identity of the latest conversation
// boundary. A change while a session-send acceptance is pending means some
// other prompt consumed the pane.
type deliveryCheckpointIdentity struct {
	state      domain.ConversationCheckpointState
	generation string
	turnID     string
}

func deliveryCheckpointIdentityOf(rec domain.SessionRecord) deliveryCheckpointIdentity {
	return deliveryCheckpointIdentity{
		state:      rec.Metadata.ConversationCheckpointState,
		generation: rec.Metadata.ConversationCheckpointGeneration,
		turnID:     rec.Metadata.ConversationCheckpointTurnID,
	}
}

// awaitSessionDeliveryAccepted confirms that a wrapped session send reached the
// agent, not just its pane. The pane write reports success the moment the bytes
// leave the daemon, but a TUI at a turn boundary can drop the whole
// paste+Enter burst and a pty-host can stall; in both cases ao send exited 0
// while the agent never saw the message (issue #6685).
//
// Harnesses that declare semantic acceptance emit a prompt-submit hook whose
// payload carries the accepted prompt, so the durable conversation checkpoint
// names the wrapped delivery id exactly when the agent accepted THIS message —
// the same evidence SendSemantic already requires of report submits. The wait
// polls that checkpoint and, while the session sits idle and unaccepted,
// re-pastes the wrapped message (bounded by sendAcceptance.maxPastes): an
// Enter-only nudge cannot recover a paste that never landed, only confirmActive's
// draft-submit case.
//
// It fails the send only when the message provably never became a prompt:
// every paste exhausted with the session still idle and the checkpoint
// unchanged. A checkpoint that moved to any newer boundary (another
// coordination send, a human prompt) ends the wait as delivered-or-superseded;
// an active or blocked pane ends it too — the running turn or pending dialog is
// itself the visible delivery state, and another paste there risks a duplicate
// turn or an answered decision.
func (m *Manager) awaitSessionDeliveryAccepted(ctx context.Context, id domain.SessionID, deliveryID, message string) error {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return fmt.Errorf("send %s: acceptance: %w", id, err)
	}
	if !ok {
		return ErrNotFound
	}
	if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeTUI || !m.harnessSemanticAcceptance(rec.Harness) {
		// The wrap id is only correlatable on a harness whose hook reports the
		// accepted prompt; anything else keeps today's pane-write semantics.
		return nil
	}
	baseline := deliveryCheckpointIdentityOf(rec)
	for paste := 1; ; paste++ {
		accepted, err := m.awaitSessionDeliveryWindow(ctx, id, deliveryID, baseline)
		if err != nil || accepted {
			return err
		}
		if paste >= m.sendAcceptance.maxPastes {
			return fmt.Errorf("send %s: %w: delivery id %s was never accepted and the session stayed idle", id, ErrSendDeliveryUnconfirmed, deliveryID)
		}
		outcome, deliverErr := m.messenger.Deliver(ctx, id, message)
		if deliverErr != nil {
			return fmt.Errorf("send %s: acceptance re-paste: %w", id, deliverErr)
		}
		switch outcome {
		case sessionguard.Sent:
			// The original paste was likely swallowed whole; one recovery paste is
			// in and gets its own evidence window.
		case sessionguard.SuppressedTerminated:
			return fmt.Errorf("send %s: %w", id, ErrTerminated)
		case sessionguard.SuppressedExited:
			return fmt.Errorf("send %s: %w", id, ErrAgentExited)
		default:
			// Blocked on a decision or input-gated: the pane shows a state the
			// human must act on anyway, and the unsubmitted draft sits beneath it.
			m.logger.Warn("send: acceptance re-paste suppressed",
				"sessionID", id, "deliveryID", deliveryID, "outcome", outcome.String())
			return nil
		}
	}
}

// awaitSessionDeliveryWindow polls the durable checkpoint for one pane write's
// evidence window. accepted is true when this message was accepted or a newer
// boundary superseded it; false means the window expired with the session
// unaccepted and unchanged.
func (m *Manager) awaitSessionDeliveryWindow(ctx context.Context, id domain.SessionID, deliveryID string, baseline deliveryCheckpointIdentity) (bool, error) {
	deadline := time.Now().Add(m.sendAcceptance.wait)
	for {
		rec, ok, err := m.store.GetSession(ctx, id)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, ErrNotFound
		}
		if rec.IsTerminated {
			return false, fmt.Errorf("send %s: %w", id, ErrTerminated)
		}
		if rec.Activity.State == domain.ActivityExited {
			return false, fmt.Errorf("send %s: %w", id, ErrAgentExited)
		}
		if semanticMessageAccepted(rec, deliveryID) {
			return true, nil
		}
		switch rec.Activity.State {
		case domain.ActivityActive, domain.ActivityBlocked:
			// The pane is busy: a running turn or a pending decision is itself
			// the visible delivery state, and the paste is either submitted or
			// parked in the composer beneath it. Waiting out the window here
			// only invites a duplicate re-paste into a busy composer and a
			// false unconfirmed error for delivery that is still pending.
			return true, nil
		}
		if current := deliveryCheckpointIdentityOf(rec); current != baseline {
			// Another prompt consumed the pane while ours was pending.
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(m.sendAcceptance.pollInterval):
		}
	}
}
