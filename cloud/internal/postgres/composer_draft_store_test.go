package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

func (c composerFixture) draftAsSeenBy(t *testing.T, who domain.Principal) *domain.ComposerDraft {
	t.Helper()
	state, err := c.store.SessionLiveState(context.Background(), who, c.fixture.orgID, c.fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	return state.Composer.Draft
}

func (c composerFixture) publish(who domain.Principal, epoch int64, text string, cursor int) (domain.ComposerLease, error) {
	return c.store.PublishComposerDraft(context.Background(), who, c.fixture.orgID, c.fixture.sessionID, epoch, text, cursor)
}

// The holder's draft and caret reach everyone in the session, read-only
// viewers included, and each change replaces the last.
func TestComposerDraftIsSharedLive(t *testing.T) {
	c := openComposerFixture(t)
	lease := c.mustAcquire(t, c.bob)
	if got := c.draftAsSeenBy(t, c.alice); got != nil {
		t.Fatalf("draft before any typing = %+v", got)
	}
	if _, err := c.publish(c.bob, lease.Epoch, "fix the fl", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := c.publish(c.bob, lease.Epoch, "fix the flaky test", 18); err != nil {
		t.Fatal(err)
	}
	for _, who := range []domain.Principal{c.alice, c.viewer, c.owner} {
		got := c.draftAsSeenBy(t, who)
		if got == nil || got.Text != "fix the flaky test" || got.Cursor != 18 {
			t.Fatalf("draft seen by %s = %+v", who.DisplayName, got)
		}
	}
}

// Only the holder's own text is ever shown: anyone else, or the holder on an
// earlier grant, is refused and the shared draft is untouched.
func TestComposerDraftOnlyFromTheHolder(t *testing.T) {
	c := openComposerFixture(t)
	lease := c.mustAcquire(t, c.bob)
	if _, err := c.publish(c.bob, lease.Epoch, "mine", 4); err != nil {
		t.Fatal(err)
	}
	if _, err := c.publish(c.alice, lease.Epoch, "not yours", 9); !errors.Is(err, ErrComposerLeaseLost) {
		t.Fatalf("Alice publishing over Bob: %v", err)
	}
	if _, err := c.publish(c.bob, lease.Epoch-1, "stale", 5); !errors.Is(err, ErrComposerLeaseLost) {
		t.Fatalf("Bob publishing on an old epoch: %v", err)
	}
	if _, err := c.publish(c.viewer, lease.Epoch, "viewer", 6); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a viewer publishing: %v", err)
	}
	if got := c.draftAsSeenBy(t, c.alice); got == nil || got.Text != "mine" {
		t.Fatalf("draft after refused publishes = %+v", got)
	}
	if _, err := c.publish(c.bob, lease.Epoch, strings.Repeat("x", MaxComposerDraftBytes+1), 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an oversized draft: %v", err)
	}
	if _, err := c.publish(c.bob, lease.Epoch, "x", -1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a negative cursor: %v", err)
	}
}

// A draft belongs to one holder's turn: sending it, releasing the lease, or
// losing it to a take-over clears it.
func TestComposerDraftClearsWhenTheTurnEnds(t *testing.T) {
	c := openComposerFixture(t)
	ctx := context.Background()

	lease := c.mustAcquire(t, c.bob)
	if _, err := c.publish(c.bob, lease.Epoch, "sending this", 12); err != nil {
		t.Fatal(err)
	}
	if _, err := c.send(c.bob, lease.Epoch); err != nil {
		t.Fatal(err)
	}
	if got := c.draftAsSeenBy(t, c.alice); got != nil {
		t.Fatalf("draft after the send = %+v", got)
	}

	lease = c.mustAcquire(t, c.bob)
	if _, err := c.publish(c.bob, lease.Epoch, "never mind", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.ReleaseComposerLease(ctx, c.bob, c.fixture.orgID, c.fixture.sessionID, lease.Epoch); err != nil {
		t.Fatal(err)
	}
	if got := c.draftAsSeenBy(t, c.alice); got != nil {
		t.Fatalf("draft after the release = %+v", got)
	}

	lease = c.mustAcquire(t, c.bob)
	if _, err := c.publish(c.bob, lease.Epoch, "half", 4); err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.TakeOverComposerLease(ctx, c.owner, c.fixture.orgID, c.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if got := c.draftAsSeenBy(t, c.alice); got != nil {
		t.Fatalf("draft after the take-over = %+v", got)
	}
}

// Publishing counts as typing: it renews the grant, so a holder who keeps
// typing keeps the lease without separate renewals.
func TestComposerDraftRenewsTheGrant(t *testing.T) {
	c := openComposerFixture(t)
	lease := c.mustAcquire(t, c.bob)
	c.expire(t)
	execOneRowAsOrg(t, c.admin, c.fixture.orgID, `UPDATE ao_composer_leases SET last_input_at = now() - interval '40 seconds'
		WHERE org_id = $1 AND session_id = $2`, c.fixture.orgID, c.fixture.sessionID)
	renewed, err := c.publish(c.bob, lease.Epoch, "still here", 10)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.ExpiresIn < ComposerLeaseTTL-2*time.Second {
		t.Fatalf("grant after publishing expires in %s", renewed.ExpiresIn)
	}
	var idleFor time.Duration
	if err := c.admin.QueryRow(context.Background(), `SELECT now() - last_input_at FROM ao_composer_leases WHERE session_id = $1`,
		c.fixture.sessionID).Scan(&idleFor); err != nil {
		t.Fatal(err)
	}
	if idleFor > 2*time.Second {
		t.Fatalf("last input %s ago after publishing", idleFor)
	}
}

// A draft change wakes the session's live streams.
func TestComposerDraftNotifiesLiveListeners(t *testing.T) {
	c := openComposerFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lease := c.mustAcquire(t, c.bob)
	conn, err := c.admin.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `LISTEN ao_session_live`); err != nil {
		t.Fatal(err)
	}
	if _, err := c.publish(c.bob, lease.Epoch, "typing", 6); err != nil {
		t.Fatal(err)
	}
	for {
		notification, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			t.Fatalf("no notification for the draft: %v", err)
		}
		if notification.Payload == c.fixture.sessionID {
			return
		}
	}
}
