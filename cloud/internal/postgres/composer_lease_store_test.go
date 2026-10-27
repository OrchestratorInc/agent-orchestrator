package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// composerFixture is a session shared with two editors, Alice and Bob, and a
// read-only Viewer; its owner is the fixture's organization owner.
type composerFixture struct {
	store   *Store
	admin   *pgxpool.Pool
	fixture notificationFixture
	owner   domain.Principal
	alice   domain.Principal
	bob     domain.Principal
	viewer  domain.Principal
}

func openComposerFixture(t *testing.T) composerFixture {
	t.Helper()
	store, admin, fixture := openNotificationTestStore(t)
	return composerFixture{
		store: store, admin: admin, fixture: fixture,
		owner:  domain.Principal{UserID: fixture.userID, Provider: "local"},
		alice:  shareSessionWith(t, store, admin, fixture, "editor", "Alice"),
		bob:    shareSessionWith(t, store, admin, fixture, "editor", "Bob"),
		viewer: shareSessionWith(t, store, admin, fixture, "viewer", "Viewer"),
	}
}

func (c composerFixture) acquire(t *testing.T, who domain.Principal) (domain.ComposerLease, error) {
	t.Helper()
	return c.store.AcquireComposerLease(context.Background(), who, c.fixture.orgID, c.fixture.sessionID)
}

func (c composerFixture) mustAcquire(t *testing.T, who domain.Principal) domain.ComposerLease {
	t.Helper()
	lease, err := c.acquire(t, who)
	if err != nil {
		t.Fatalf("acquire for %s: %v", who.DisplayName, err)
	}
	if lease.Holder == nil || lease.Holder.UserID != who.UserID {
		t.Fatalf("acquire for %s: holder = %+v", who.DisplayName, lease.Holder)
	}
	return lease
}

// present opens a live stream for who, so the waitlist counts them present.
func (c composerFixture) present(t *testing.T, who domain.Principal) {
	t.Helper()
	if err := c.store.JoinSessionViewers(context.Background(), who, c.fixture.orgID, c.fixture.sessionID, uuid.NewString()); err != nil {
		t.Fatalf("join viewers: %v", err)
	}
}

func (c composerFixture) send(who domain.Principal, epoch int64) (domain.ClientEvent, error) {
	return c.store.SendMessageWithLease(context.Background(), who, c.fixture.orgID, c.fixture.sessionID,
		uuid.NewString(), "hello from "+who.DisplayName, domain.ChatTurnSettings{}, epoch)
}

// expire moves the session's grant into the past, as if its holder stopped
// renewing.
func (c composerFixture) expire(t *testing.T) {
	t.Helper()
	execOneRowAsOrg(t, c.admin, c.fixture.orgID, `UPDATE ao_composer_leases SET expires_at = now() - interval '1 second'
		WHERE org_id = $1 AND session_id = $2`, c.fixture.orgID, c.fixture.sessionID)
}

func holderOf(lease domain.ComposerLease) string {
	if lease.Holder == nil {
		return ""
	}
	return lease.Holder.UserID
}

// When many people start typing at the same moment, exactly one gets the
// lease, round after round.
func TestComposerAcquireRaceHasOneWinner(t *testing.T) {
	c := openComposerFixture(t)
	ctx := context.Background()
	contenders := []domain.Principal{c.owner, c.alice, c.bob}
	for len(contenders) < 10 {
		contenders = append(contenders, shareSessionWith(t, c.store, c.admin, c.fixture, "editor", "Racer"))
	}
	for round := 0; round < 10; round++ {
		var wg sync.WaitGroup
		start := make(chan struct{})
		leases := make([]domain.ComposerLease, len(contenders))
		results := make([]error, len(contenders))
		for i, who := range contenders {
			wg.Add(1)
			go func(i int, who domain.Principal) {
				defer wg.Done()
				<-start
				leases[i], results[i] = c.acquire(t, who)
			}(i, who)
		}
		close(start)
		wg.Wait()
		winner := -1
		for i, err := range results {
			switch {
			case err == nil:
				if winner >= 0 {
					t.Fatalf("round %d: contenders %d and %d both got the lease", round, winner, i)
				}
				winner = i
			case errors.Is(err, ErrComposerLocked):
			default:
				t.Fatalf("round %d contender %d: %v", round, i, err)
			}
		}
		if winner < 0 {
			t.Fatalf("round %d: nobody got the lease", round)
		}
		if _, err := c.store.ReleaseComposerLease(ctx, contenders[winner], c.fixture.orgID, c.fixture.sessionID, leases[winner].Epoch); err != nil {
			t.Fatal(err)
		}
	}
}

// Every lease operation holds the session's lease row lock for its whole
// transaction, so a second operation waits instead of reading a stale lease.
func TestComposerLeaseRowIsLockedForTheTransaction(t *testing.T) {
	c := openComposerFixture(t)
	ctx := context.Background()
	c.mustAcquire(t, c.alice)
	lockIn := func() (pgx.Tx, error) {
		tx, err := c.admin.Begin(ctx)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('ao.org_id', $1, true), set_config('lock_timeout', '200ms', true)`, c.fixture.orgID); err != nil {
			_ = tx.Rollback(ctx)
			return nil, err
		}
		if _, err := lockComposerLeaseTx(ctx, tx, c.fixture.orgID, c.fixture.sessionID); err != nil {
			_ = tx.Rollback(ctx)
			return nil, err
		}
		return tx, nil
	}
	first, err := lockIn()
	if err != nil {
		t.Fatal(err)
	}
	defer first.Rollback(ctx)
	if second, err := lockIn(); err == nil {
		_ = second.Rollback(ctx)
		t.Fatal("a second transaction locked the lease row while the first held it")
	}
	if err := first.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := lockIn()
	if err != nil {
		t.Fatalf("locking after the first transaction ended: %v", err)
	}
	_ = second.Rollback(ctx)
}

// The holder can keep and use the lease; everyone else is refused and can
// see who has it. Sending releases it.
func TestComposerLeaseBlocksOthersUntilTheHolderSends(t *testing.T) {
	c := openComposerFixture(t)
	lease := c.mustAcquire(t, c.alice)
	if lease.Holder.Name != "Alice" || lease.ExpiresIn <= 0 || lease.ExpiresIn > ComposerLeaseTTL {
		t.Fatalf("granted lease = %+v", lease)
	}
	again := c.mustAcquire(t, c.alice)
	if again.Epoch != lease.Epoch {
		t.Fatalf("re-acquiring changed the epoch from %d to %d", lease.Epoch, again.Epoch)
	}

	refused, err := c.acquire(t, c.bob)
	if !errors.Is(err, ErrComposerLocked) || holderOf(refused) != c.alice.UserID {
		t.Fatalf("Bob's acquire = %+v, %v", refused, err)
	}
	for name, epoch := range map[string]int64{"no grant": 0, "Alice's epoch": lease.Epoch} {
		if _, err := c.send(c.bob, epoch); !errors.Is(err, ErrComposerLocked) {
			t.Fatalf("Bob sending with %s: %v, want ErrComposerLocked", name, err)
		}
	}
	if _, err := c.send(c.alice, lease.Epoch); err != nil {
		t.Fatalf("Alice's send: %v", err)
	}
	// Alice's turn ended with her send; the lease is free for anyone.
	if _, err := c.send(c.alice, lease.Epoch); err != nil {
		t.Fatalf("send while free: %v", err)
	}
	c.mustAcquire(t, c.bob)
}

// A send with no grant (an older client, a button) works only while the
// lease is free and nobody is waiting.
func TestComposerUngrantedSendNeedsAFreeLease(t *testing.T) {
	c := openComposerFixture(t)
	if _, err := c.send(c.bob, 0); err != nil {
		t.Fatalf("send while free: %v", err)
	}
	c.present(t, c.alice)
	lease := c.mustAcquire(t, c.bob)
	if _, err := c.store.RequestComposerTurn(context.Background(), c.alice, c.fixture.orgID, c.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.ReleaseComposerLease(context.Background(), c.bob, c.fixture.orgID, c.fixture.sessionID, lease.Epoch); err != nil {
		t.Fatal(err)
	}
	// Alice was waiting, so the released lease went to her.
	if _, err := c.send(c.owner, 0); !errors.Is(err, ErrComposerLocked) {
		t.Fatalf("ungranted send while Alice has the lease: %v", err)
	}
}

// Releasing, or sending, hands the lease to the first person in line, under a
// new epoch and a short window to start typing.
func TestComposerReleaseHandsOffToTheWaitlist(t *testing.T) {
	c := openComposerFixture(t)
	ctx := context.Background()
	c.present(t, c.bob)
	c.present(t, c.owner)
	alice := c.mustAcquire(t, c.alice)

	queued, err := c.store.RequestComposerTurn(ctx, c.bob, c.fixture.orgID, c.fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.RequestComposerTurn(ctx, c.owner, c.fixture.orgID, c.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if len(queued.Waitlist) != 1 || queued.Waitlist[0].Name != "Bob" {
		t.Fatalf("waitlist after Bob asked = %+v", queued.Waitlist)
	}

	released, err := c.store.ReleaseComposerLease(ctx, c.alice, c.fixture.orgID, c.fixture.sessionID, alice.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	if holderOf(released) != c.bob.UserID || released.Epoch <= alice.Epoch || released.ExpiresIn > ComposerHandoffWindow {
		t.Fatalf("after release = %+v, want Bob holding a new epoch within the handoff window", released)
	}
	if len(released.Waitlist) != 1 || released.Waitlist[0].UserID != c.owner.UserID {
		t.Fatalf("waitlist after handoff = %+v", released.Waitlist)
	}

	// Bob sends; the owner is next.
	if _, err := c.send(c.bob, released.Epoch); err != nil {
		t.Fatal(err)
	}
	state, err := c.store.SessionLiveState(ctx, c.alice, c.fixture.orgID, c.fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if holderOf(state.Composer) != c.owner.UserID || len(state.Composer.Waitlist) != 0 {
		t.Fatalf("after Bob's send = %+v", state.Composer)
	}
}

// Someone who stopped watching the session is skipped when their turn comes.
func TestComposerHandoffSkipsAbsentWaiters(t *testing.T) {
	c := openComposerFixture(t)
	ctx := context.Background()
	alice := c.mustAcquire(t, c.alice)
	// Bob asks first but his only stream went quiet (its replica died); the
	// owner is present.
	c.present(t, c.bob)
	if _, err := c.store.RequestComposerTurn(ctx, c.bob, c.fixture.orgID, c.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.admin.Exec(ctx, `UPDATE ao_session_viewers SET seen_at = now() - $2::interval WHERE user_id = $1`,
		c.bob.UserID, intervalString(SessionViewerTTL+time.Second)); err != nil {
		t.Fatal(err)
	}
	c.present(t, c.owner)
	if _, err := c.store.RequestComposerTurn(ctx, c.owner, c.fixture.orgID, c.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	released, err := c.store.ReleaseComposerLease(ctx, c.alice, c.fixture.orgID, c.fixture.sessionID, alice.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	if holderOf(released) != c.owner.UserID || len(released.Waitlist) != 0 {
		t.Fatalf("after release = %+v, want the owner holding and Bob dropped", released)
	}
}

// A take-over fences off the previous holder: their old epoch can neither
// send nor renew. Only the session creator and organization owners and admins
// may take over.
func TestComposerTakeOverFencesThePreviousHolder(t *testing.T) {
	c := openComposerFixture(t)
	ctx := context.Background()
	alice := c.mustAcquire(t, c.alice)

	if _, err := c.store.TakeOverComposerLease(ctx, c.bob, c.fixture.orgID, c.fixture.sessionID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("an editor's take-over: %v, want ErrForbidden", err)
	}
	// The live state tells each person whether they may take over.
	for who, want := range map[*domain.Principal]bool{&c.bob: false, &c.owner: true} {
		state, err := c.store.SessionLiveState(ctx, *who, c.fixture.orgID, c.fixture.sessionID)
		if err != nil || state.CanTakeOver != want {
			t.Fatalf("canTakeOver for %s = %v, %v; want %v", who.UserID, state.CanTakeOver, err, want)
		}
	}
	taken, err := c.store.TakeOverComposerLease(ctx, c.owner, c.fixture.orgID, c.fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if holderOf(taken) != c.owner.UserID || taken.Epoch <= alice.Epoch {
		t.Fatalf("after take-over = %+v", taken)
	}
	if _, err := c.send(c.alice, alice.Epoch); !errors.Is(err, ErrComposerLocked) {
		t.Fatalf("Alice's stale send: %v, want ErrComposerLocked", err)
	}
	if _, err := c.store.RenewComposerLease(ctx, c.alice, c.fixture.orgID, c.fixture.sessionID, alice.Epoch, true); !errors.Is(err, ErrComposerLeaseLost) {
		t.Fatalf("Alice's stale renew: %v, want ErrComposerLeaseLost", err)
	}
	if _, err := c.send(c.owner, taken.Epoch); err != nil {
		t.Fatalf("the owner's send: %v", err)
	}

	// An organization admin may take over too.
	admin := seedTestUser(t, c.admin, "Admin")
	execOneRowAsOrg(t, c.admin, c.fixture.orgID, `INSERT INTO ao_org_memberships (org_id, user_id, role) VALUES ($1, $2, 'admin')`,
		c.fixture.orgID, admin.UserID)
	t.Cleanup(func() {
		execAsOrg(c.admin, c.fixture.orgID, `DELETE FROM ao_org_memberships WHERE org_id = $1 AND user_id = $2`, c.fixture.orgID, admin.UserID)
	})
	if _, err := c.store.TakeOverComposerLease(ctx, admin, c.fixture.orgID, c.fixture.sessionID); err != nil {
		t.Fatalf("an admin's take-over: %v", err)
	}
}

// A grant that lapses stands while nobody is waiting, so a holder whose
// renewals were briefly delayed loses nothing; once someone present is
// waiting, the lapsed grant is gone.
func TestComposerLapsedGrantStandsUntilSomeoneWaits(t *testing.T) {
	c := openComposerFixture(t)
	ctx := context.Background()
	lease := c.mustAcquire(t, c.alice)
	c.expire(t)
	renewed, err := c.store.RenewComposerLease(ctx, c.alice, c.fixture.orgID, c.fixture.sessionID, lease.Epoch, true)
	if err != nil || holderOf(renewed) != c.alice.UserID || renewed.Epoch != lease.Epoch {
		t.Fatalf("renewing a lapsed grant with nobody waiting = %+v, %v", renewed, err)
	}
	c.expire(t)
	if _, err := c.send(c.alice, lease.Epoch); err != nil {
		t.Fatalf("sending on a lapsed grant with nobody waiting: %v", err)
	}

	// Bob gets in line while Alice's new grant is live; it then lapses before
	// any sweep. Alice's send now finds someone waiting and loses.
	lease = c.mustAcquire(t, c.alice)
	c.present(t, c.bob)
	if _, err := c.store.RequestComposerTurn(ctx, c.bob, c.fixture.orgID, c.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	c.expire(t)
	if _, err := c.send(c.alice, lease.Epoch); !errors.Is(err, ErrComposerLocked) {
		t.Fatalf("Alice's send on a lapsed grant with Bob waiting: %v, want ErrComposerLocked", err)
	}
	if _, err := c.store.RenewComposerLease(ctx, c.alice, c.fixture.orgID, c.fixture.sessionID, lease.Epoch, true); !errors.Is(err, ErrComposerLeaseLost) {
		t.Fatalf("Alice's renew on a lapsed grant with Bob waiting: %v, want ErrComposerLeaseLost", err)
	}
}

// An epoch from an earlier grant is refused even when its owner holds the
// lease again, so a stale tab or retried request cannot act on a grant that
// ended.
func TestComposerRefusesAnEarlierEpochOfTheSameHolder(t *testing.T) {
	c := openComposerFixture(t)
	ctx := context.Background()
	first := c.mustAcquire(t, c.alice)
	if _, err := c.store.ReleaseComposerLease(ctx, c.alice, c.fixture.orgID, c.fixture.sessionID, first.Epoch); err != nil {
		t.Fatal(err)
	}
	second := c.mustAcquire(t, c.alice)
	if second.Epoch <= first.Epoch {
		t.Fatalf("a new grant reused epoch %d", first.Epoch)
	}
	if _, err := c.send(c.alice, first.Epoch); !errors.Is(err, ErrComposerLocked) {
		t.Fatalf("send on the earlier epoch: %v, want ErrComposerLocked", err)
	}
	if _, err := c.store.RenewComposerLease(ctx, c.alice, c.fixture.orgID, c.fixture.sessionID, first.Epoch, true); !errors.Is(err, ErrComposerLeaseLost) {
		t.Fatalf("renew on the earlier epoch: %v, want ErrComposerLeaseLost", err)
	}
	// Releasing the earlier epoch must not release the current grant.
	after, err := c.store.ReleaseComposerLease(ctx, c.alice, c.fixture.orgID, c.fixture.sessionID, first.Epoch)
	if err != nil || holderOf(after) != c.alice.UserID || after.Epoch != second.Epoch {
		t.Fatalf("release on the earlier epoch = %+v, %v", after, err)
	}
}

// A holder who stops typing loses the lease even while their client keeps
// renewing it.
func TestComposerIdleHolderLosesTheLease(t *testing.T) {
	c := openComposerFixture(t)
	ctx := context.Background()
	lease := c.mustAcquire(t, c.alice)
	execOneRowAsOrg(t, c.admin, c.fixture.orgID, `UPDATE ao_composer_leases SET last_input_at = now() - $3::interval
		WHERE org_id = $1 AND session_id = $2`, c.fixture.orgID, c.fixture.sessionID, intervalString(ComposerIdleRelease+time.Second))
	idle, err := c.store.RenewComposerLease(ctx, c.alice, c.fixture.orgID, c.fixture.sessionID, lease.Epoch, false)
	if !errors.Is(err, ErrComposerLeaseLost) || idle.Holder != nil {
		t.Fatalf("idle renew = %+v, %v", idle, err)
	}

	// Typing keeps it: a renewal that reports a keystroke resets the clock.
	lease = c.mustAcquire(t, c.alice)
	execOneRowAsOrg(t, c.admin, c.fixture.orgID, `UPDATE ao_composer_leases SET last_input_at = now() - $3::interval
		WHERE org_id = $1 AND session_id = $2`, c.fixture.orgID, c.fixture.sessionID, intervalString(ComposerIdleRelease+time.Second))
	if _, err := c.store.RenewComposerLease(ctx, c.alice, c.fixture.orgID, c.fixture.sessionID, lease.Epoch, true); err != nil {
		t.Fatalf("typing renew: %v", err)
	}
}

// The sweep hands an expired lease to the next person in line, or frees it.
func TestComposerSweepSettlesExpiredLeases(t *testing.T) {
	c := openComposerFixture(t)
	ctx := context.Background()
	readComposer := func() domain.ComposerLease {
		t.Helper()
		state, err := c.store.SessionLiveState(ctx, c.owner, c.fixture.orgID, c.fixture.sessionID)
		if err != nil {
			t.Fatal(err)
		}
		return state.Composer
	}

	alice := c.mustAcquire(t, c.alice)
	c.present(t, c.bob)
	if _, err := c.store.RequestComposerTurn(ctx, c.bob, c.fixture.orgID, c.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	c.expire(t)
	if _, err := c.store.SweepComposerLeases(ctx); err != nil {
		t.Fatal(err)
	}
	if got := readComposer(); holderOf(got) != c.bob.UserID || got.Epoch <= alice.Epoch {
		t.Fatalf("after sweeping with Bob waiting = %+v", got)
	}

	c.expire(t)
	if _, err := c.store.SweepComposerLeases(ctx); err != nil {
		t.Fatal(err)
	}
	if got := readComposer(); got.Holder != nil {
		t.Fatalf("after sweeping with nobody waiting = %+v", got)
	}
}

// Read-only participants can neither take the lease nor queue for it.
func TestComposerViewersCannotTakePart(t *testing.T) {
	c := openComposerFixture(t)
	ctx := context.Background()
	if _, err := c.acquire(t, c.viewer); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer acquire: %v", err)
	}
	if _, err := c.store.RequestComposerTurn(ctx, c.viewer, c.fixture.orgID, c.fixture.sessionID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer queue: %v", err)
	}
	if _, err := c.send(c.viewer, 0); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer send: %v", err)
	}
	// They still see the lease.
	c.mustAcquire(t, c.alice)
	state, err := c.store.SessionLiveState(ctx, c.viewer, c.fixture.orgID, c.fixture.sessionID)
	if err != nil || state.Composer.Holder == nil || state.Composer.Holder.Name != "Alice" {
		t.Fatalf("viewer's live state = %+v, %v", state.Composer, err)
	}
}

// Retrying a message that was already sent returns it, even though its send
// released the lease and someone else holds it now.
func TestComposerIdempotentRetryNeedsNoLease(t *testing.T) {
	c := openComposerFixture(t)
	ctx := context.Background()
	lease := c.mustAcquire(t, c.alice)
	key := uuid.NewString()
	sent, err := c.store.SendMessageWithLease(ctx, c.alice, c.fixture.orgID, c.fixture.sessionID, key, "once", domain.ChatTurnSettings{}, lease.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	c.mustAcquire(t, c.bob)
	retried, err := c.store.SendMessageWithLease(ctx, c.alice, c.fixture.orgID, c.fixture.sessionID, key, "once", domain.ChatTurnSettings{}, lease.Epoch)
	if err != nil || retried.Sequence != sent.Sequence {
		t.Fatalf("retry = %+v, %v; want sequence %d", retried, err, sent.Sequence)
	}
}

// Messages from orchestrators and automation never wait for the lease.
func TestComposerDoesNotGateAutomatedMessages(t *testing.T) {
	c := openComposerFixture(t)
	ctx := context.Background()
	c.mustAcquire(t, c.alice)
	err := c.store.withOrg(ctx, c.fixture.orgID, func(tx pgx.Tx) error {
		_, err := sendMessageTx(ctx, tx, c.fixture.orgID, c.fixture.sessionID, "auto-"+uuid.NewString(), "CI failed",
			"", c.fixture.sessionID, "", nil, domain.ChatTurnSettings{}, false)
		return err
	})
	if err != nil {
		t.Fatalf("automated message while Alice holds the lease: %v", err)
	}
}

// Steering a running turn needs the lease like sending does.
func TestComposerGatesSteering(t *testing.T) {
	c := openComposerFixture(t)
	ctx := context.Background()
	lease := c.mustAcquire(t, c.alice)
	turnID := uuid.NewString()
	if _, err := c.store.SteerTurn(ctx, c.bob, c.fixture.orgID, c.fixture.sessionID, turnID, uuid.NewString(), "stop", 0); !errors.Is(err, ErrComposerLocked) {
		t.Fatalf("Bob's steer: %v, want ErrComposerLocked", err)
	}
	// Alice passes the lease check and reaches the turn lookup.
	if _, err := c.store.SteerTurn(ctx, c.alice, c.fixture.orgID, c.fixture.sessionID, turnID, uuid.NewString(), "stop", lease.Epoch); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Alice's steer of a missing turn: %v, want ErrNotFound", err)
	}
}

// Lease changes others can see notify the session's live streams; a renewal
// does not.
func TestComposerChangesNotifyLiveListeners(t *testing.T) {
	c := openComposerFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := c.admin.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `LISTEN ao_session_live`); err != nil {
		t.Fatal(err)
	}
	next := func(within time.Duration) bool {
		t.Helper()
		waitCtx, stop := context.WithTimeout(ctx, within)
		defer stop()
		for {
			notification, err := conn.Conn().WaitForNotification(waitCtx)
			if err != nil {
				return false
			}
			if notification.Payload == c.fixture.sessionID {
				return true
			}
		}
	}
	lease := c.mustAcquire(t, c.alice)
	if !next(5 * time.Second) {
		t.Fatal("a grant did not notify")
	}
	if _, err := c.store.RenewComposerLease(ctx, c.alice, c.fixture.orgID, c.fixture.sessionID, lease.Epoch, true); err != nil {
		t.Fatal(err)
	}
	if next(300 * time.Millisecond) {
		t.Fatal("a renewal notified")
	}
	if _, err := c.store.RequestComposerTurn(ctx, c.bob, c.fixture.orgID, c.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if !next(5 * time.Second) {
		t.Fatal("joining the waitlist did not notify")
	}
}
