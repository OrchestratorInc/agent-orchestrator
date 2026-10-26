package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// chatTurnBy has who send a message in a Chat session and returns the turn it
// queued.
func chatTurnBy(t *testing.T, c composerFixture, who domain.Principal) string {
	t.Helper()
	ctx := context.Background()
	lease := c.mustAcquire(t, who)
	event, err := c.send(who, lease.Epoch)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	var turnID string
	if err := c.admin.QueryRow(ctx, `SELECT id::text FROM ao_turns WHERE session_id = $1 AND user_message_sequence = $2`,
		c.fixture.sessionID, event.Sequence).Scan(&turnID); err != nil {
		t.Fatalf("find the message's turn: %v", err)
	}
	return turnID
}

func openChatComposerFixture(t *testing.T) composerFixture {
	t.Helper()
	c := openComposerFixture(t)
	execOneRowAsOrg(t, c.admin, c.fixture.orgID, `UPDATE ao_sessions SET interface = 'chat', harness = 'claude-code'
		WHERE org_id = $1 AND id = $2`, c.fixture.orgID, c.fixture.sessionID)
	return c
}

// A turn may be stopped by the person whose message started it, the lease
// holder, the session creator, and organization owners and admins; another
// participant may not, and neither may a viewer. Each case stops one turn in
// its own session: a stopped queued turn stays active until a worker settles
// it, so a session holds only one.
func TestTurnCancellationBelongsToWhoeverDirectsTheTurn(t *testing.T) {
	ctx := context.Background()
	cancel := func(c composerFixture, who domain.Principal, turnID string) error {
		return c.store.RequestTurnCancellation(ctx, who, c.fixture.orgID, c.fixture.sessionID, turnID)
	}

	t.Run("its author, but not another participant or a viewer", func(t *testing.T) {
		c := openChatComposerFixture(t)
		turn := chatTurnBy(t, c, c.alice)
		if err := cancel(c, c.bob, turn); !errors.Is(err, ErrForbidden) {
			t.Fatalf("Bob stopping Alice's turn: %v, want ErrForbidden", err)
		}
		if err := cancel(c, c.viewer, turn); !errors.Is(err, ErrForbidden) {
			t.Fatalf("a viewer stopping Alice's turn: %v, want ErrForbidden", err)
		}
		if err := cancel(c, c.alice, turn); err != nil {
			t.Fatalf("Alice stopping her own turn: %v", err)
		}
	})
	t.Run("the lease holder", func(t *testing.T) {
		c := openChatComposerFixture(t)
		turn := chatTurnBy(t, c, c.alice)
		c.mustAcquire(t, c.bob)
		if err := cancel(c, c.bob, turn); err != nil {
			t.Fatalf("the lease holder stopping Alice's turn: %v", err)
		}
	})
	t.Run("the session creator", func(t *testing.T) {
		c := openChatComposerFixture(t)
		turn := chatTurnBy(t, c, c.alice)
		if err := cancel(c, c.owner, turn); err != nil {
			t.Fatalf("the creator stopping Alice's turn: %v", err)
		}
	})
}

// A missing creator must not turn an authorization denial into a SQL NULL
// scan error, or remove the author's and organization's control of the turn.
func TestTurnCancellationWithoutSessionCreator(t *testing.T) {
	ctx := context.Background()
	for _, actor := range []string{"author", "org owner"} {
		t.Run(actor, func(t *testing.T) {
			c := openChatComposerFixture(t)
			execOneRowAsOrg(t, c.admin, c.fixture.orgID, `UPDATE ao_sessions SET created_by_user_id = NULL WHERE org_id = $1 AND id = $2`, c.fixture.orgID, c.fixture.sessionID)
			turn := chatTurnBy(t, c, c.alice)
			for _, denied := range []domain.Principal{c.bob, c.viewer} {
				if err := c.store.RequestTurnCancellation(ctx, denied, c.fixture.orgID, c.fixture.sessionID, turn); !errors.Is(err, ErrForbidden) {
					t.Fatalf("%s stopping another participant's turn: %v, want ErrForbidden", denied.DisplayName, err)
				}
			}
			allowed := c.alice
			if actor == "org owner" {
				allowed = c.owner
			}
			if err := c.store.RequestTurnCancellation(ctx, allowed, c.fixture.orgID, c.fixture.sessionID, turn); err != nil {
				t.Fatalf("%s stopping the turn without a session creator: %v", actor, err)
			}
		})
	}
}

func mustHolder(t *testing.T, c composerFixture, who domain.Principal) domain.ComposerLease {
	t.Helper()
	state, err := c.store.SessionLiveState(context.Background(), who, c.fixture.orgID, c.fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if holderOf(state.Composer) != who.UserID {
		t.Fatalf("holder = %+v, want %s", state.Composer.Holder, who.DisplayName)
	}
	return state.Composer
}

// An approval prompt is answered by whoever may direct its turn, not by any
// participant.
func TestApprovalDecisionsBelongToWhoeverDirectsTheTurn(t *testing.T) {
	c := openChatComposerFixture(t)
	ctx := context.Background()
	chatTurnBy(t, c, c.alice)
	turn, claimed, err := c.store.ClaimWorkerTurn(ctx, c.fixture.orgID, c.fixture.sessionID, c.fixture.workerID, c.fixture.epoch)
	if err != nil || !claimed {
		t.Fatalf("claim = %v, %v", claimed, err)
	}
	requestID := uuid.NewString()
	if err := c.store.CreateWorkerChatApproval(ctx, c.fixture.orgID, c.fixture.sessionID, c.fixture.workerID, c.fixture.epoch, worker.ChatApproval{
		RequestID: requestID, TurnID: turn.ID, Attempt: turn.Attempt, WorkerEpoch: c.fixture.epoch,
		Summary: "Run the tests", Decisions: json.RawMessage(`[{"id":"allow","label":"Allow"},{"id":"deny","label":"Deny"}]`),
	}); err != nil {
		t.Fatalf("create approval: %v", err)
	}
	decide := func(who domain.Principal) error {
		return c.store.DecideChatApproval(ctx, who, c.fixture.orgID, c.fixture.sessionID, requestID, "allow")
	}
	if err := decide(c.bob); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Bob answering Alice's approval: %v, want ErrForbidden", err)
	}
	if err := decide(c.alice); err != nil {
		t.Fatalf("Alice answering her approval: %v", err)
	}
}

// Typing into the agent terminal takes the lease for the typist, renews it
// while they type, and is refused while someone else holds it or waits for it.
func TestAgentTerminalInputTakesTheComposerLease(t *testing.T) {
	c := openComposerFixture(t)
	ctx := context.Background()
	typeAs := func(who domain.Principal) bool {
		t.Helper()
		allowed, err := c.store.ComposerTerminalInput(ctx, c.fixture.orgID, c.fixture.sessionID, who.UserID)
		if err != nil {
			t.Fatal(err)
		}
		return allowed
	}

	if !typeAs(c.alice) {
		t.Fatal("Alice's keystroke on a free lease was refused")
	}
	first := mustHolder(t, c, c.alice)
	if typeAs(c.bob) {
		t.Fatal("Bob's keystroke while Alice holds the lease was admitted")
	}
	if !typeAs(c.alice) || mustHolder(t, c, c.alice).Epoch != first.Epoch {
		t.Fatal("Alice's next keystroke did not keep her grant")
	}

	// Bob waits; Alice stops typing and her grant lapses.
	c.present(t, c.bob)
	if _, err := c.store.RequestComposerTurn(ctx, c.bob, c.fixture.orgID, c.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	c.expire(t)
	if typeAs(c.alice) {
		t.Fatal("Alice's keystroke on a lapsed grant with Bob waiting was admitted")
	}
	if !typeAs(c.bob) {
		t.Fatal("Bob's keystroke after the handoff was refused")
	}
	mustHolder(t, c, c.bob)
}

// A terminal ticket remembers who it was issued to, and the terminal it opens
// carries that person, so the agent terminal knows whose keystrokes arrive.
func TestTerminalTicketCarriesItsUser(t *testing.T) {
	c := openComposerFixture(t)
	ctx := context.Background()
	execOneRowAsOrg(t, c.admin, c.fixture.orgID, `UPDATE ao_sessions SET mode = 'trusted' WHERE org_id = $1 AND id = $2`,
		c.fixture.orgID, c.fixture.sessionID)
	token, _, err := c.store.IssueTerminalTicket(ctx, c.alice, c.fixture.orgID, c.fixture.sessionID, "workspace", "", time.Minute)
	if err != nil {
		t.Fatalf("issue ticket: %v", err)
	}
	var userID string
	if err := c.admin.QueryRow(ctx, `SELECT COALESCE(user_id::text, '') FROM ao_access_tickets
		WHERE session_id = $1 ORDER BY created_at DESC LIMIT 1`, c.fixture.sessionID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if userID != c.alice.UserID {
		t.Fatalf("ticket user = %q, want Alice", userID)
	}
	terminal, err := c.store.OpenTerminal(ctx, token, "workspace", "", time.Minute)
	if err != nil {
		t.Fatalf("open terminal: %v", err)
	}
	if terminal.UserID != c.alice.UserID {
		t.Fatalf("terminal user = %q, want Alice", terminal.UserID)
	}
}
