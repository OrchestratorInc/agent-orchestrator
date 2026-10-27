package postgres

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

func liveViewers(t *testing.T, store *Store, principal domain.Principal, fixture notificationFixture) []domain.SessionViewer {
	t.Helper()
	state, err := store.SessionLiveState(context.Background(), principal, fixture.orgID, fixture.sessionID)
	if err != nil {
		t.Fatalf("live state: %v", err)
	}
	return state.Viewers
}

// A viewer list shows each person with an open live stream once, by display
// name and role, in the order they joined; it drops them when their last
// stream closes.
func TestSessionViewersJoinAndLeave(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	owner := domain.Principal{UserID: fixture.userID, Provider: "local"}
	guest := shareSessionWith(t, store, admin, fixture, "editor", "Guest Editor")
	const photo = "https://lh3.googleusercontent.com/guest"
	if _, err := admin.Exec(ctx, `UPDATE ao_users SET avatar_url = $2 WHERE id = $1`, guest.UserID, photo); err != nil {
		t.Fatal(err)
	}

	if got := liveViewers(t, store, owner, fixture); len(got) != 0 {
		t.Fatalf("viewers before anyone joined = %+v", got)
	}
	ownerTab1, ownerTab2, guestTab := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, join := range []struct {
		who domain.Principal
		id  string
	}{{owner, ownerTab1}, {owner, ownerTab2}, {guest, guestTab}} {
		if err := store.JoinSessionViewers(ctx, join.who, fixture.orgID, fixture.sessionID, join.id); err != nil {
			t.Fatalf("join: %v", err)
		}
	}
	want := []domain.SessionViewer{
		{UserID: fixture.userID, Name: "Notification Test", Role: "member"},
		{UserID: guest.UserID, Name: "Guest Editor", AvatarURL: photo, Role: "editor"},
	}
	if got := liveViewers(t, store, owner, fixture); !reflect.DeepEqual(got, want) {
		t.Fatalf("viewers = %+v, want %+v", got, want)
	}
	// The guest sees the same list.
	if got := liveViewers(t, store, guest, fixture); !reflect.DeepEqual(got, want) {
		t.Fatalf("guest's viewers = %+v, want %+v", got, want)
	}

	// Closing one of two tabs keeps the owner present.
	if err := store.LeaveSessionViewers(ctx, fixture.orgID, ownerTab1); err != nil {
		t.Fatal(err)
	}
	if got := liveViewers(t, store, owner, fixture); !reflect.DeepEqual(got, want) {
		t.Fatalf("after closing one tab = %+v", got)
	}
	if err := store.LeaveSessionViewers(ctx, fixture.orgID, ownerTab2); err != nil {
		t.Fatal(err)
	}
	if got := liveViewers(t, store, guest, fixture); !reflect.DeepEqual(got, want[1:]) {
		t.Fatalf("after the owner left = %+v", got)
	}
}

// A stream whose replica died stops counting once its row goes stale; a
// refresh keeps a live stream counted, and a later join prunes old rows.
func TestSessionViewersExpireWithoutRefresh(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	owner := domain.Principal{UserID: fixture.userID, Provider: "local"}
	connection := uuid.NewString()
	if err := store.JoinSessionViewers(ctx, owner, fixture.orgID, fixture.sessionID, connection); err != nil {
		t.Fatal(err)
	}
	age := func(by time.Duration) {
		t.Helper()
		execOneRowAsOrg(t, admin, fixture.orgID, `UPDATE ao_session_viewers SET seen_at = now() - $2::interval
			WHERE org_id = $1 AND connection_id = $3`, fixture.orgID, intervalString(by), connection)
	}

	age(SessionViewerTTL + time.Second)
	if got := liveViewers(t, store, owner, fixture); len(got) != 0 {
		t.Fatalf("stale viewer still listed: %+v", got)
	}
	if err := store.RefreshSessionViewer(ctx, fixture.orgID, connection); err != nil {
		t.Fatal(err)
	}
	if got := liveViewers(t, store, owner, fixture); len(got) != 1 {
		t.Fatalf("refreshed viewer not listed: %+v", got)
	}

	age(10 * SessionViewerTTL)
	if err := store.JoinSessionViewers(ctx, owner, fixture.orgID, fixture.sessionID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM ao_session_viewers WHERE connection_id = $1`, connection).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatal("a join did not prune a long-dead stream's row")
	}
}

// Someone without access to the session can neither join nor read its
// viewers, and a share viewer joins with the viewer role.
func TestSessionViewersRequireSessionAccess(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	stranger := seedTestUser(t, admin, "Stranger")
	if err := store.JoinSessionViewers(ctx, stranger, fixture.orgID, fixture.sessionID, uuid.NewString()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stranger join error = %v, want ErrForbidden", err)
	}
	if _, err := store.SessionLiveState(ctx, stranger, fixture.orgID, fixture.sessionID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stranger read error = %v, want ErrForbidden", err)
	}

	watcher := shareSessionWith(t, store, admin, fixture, "viewer", "Read Only")
	if err := store.JoinSessionViewers(ctx, watcher, fixture.orgID, fixture.sessionID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	got := liveViewers(t, store, watcher, fixture)
	if len(got) != 1 || got[0].Role != "viewer" || got[0].Name != "Read Only" {
		t.Fatalf("viewers = %+v", got)
	}
}

// Joining and leaving notify ao_session_live; a refresh, which changes
// nothing anyone sees, does not.
func TestSessionViewerChangesNotifyLiveListeners(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := admin.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `LISTEN ao_session_live`); err != nil {
		t.Fatal(err)
	}
	// nextForSession waits for this session's next notification, skipping
	// other tests' sessions, and reports false when none arrives in time.
	nextForSession := func(within time.Duration) bool {
		t.Helper()
		waitCtx, stop := context.WithTimeout(ctx, within)
		defer stop()
		for {
			notification, err := conn.Conn().WaitForNotification(waitCtx)
			if err != nil {
				return false
			}
			if notification.Payload == fixture.sessionID {
				return true
			}
		}
	}

	owner := domain.Principal{UserID: fixture.userID, Provider: "local"}
	connection := uuid.NewString()
	if err := store.JoinSessionViewers(ctx, owner, fixture.orgID, fixture.sessionID, connection); err != nil {
		t.Fatal(err)
	}
	if !nextForSession(5 * time.Second) {
		t.Fatal("join did not notify")
	}
	if err := store.RefreshSessionViewer(ctx, fixture.orgID, connection); err != nil {
		t.Fatal(err)
	}
	if nextForSession(300 * time.Millisecond) {
		t.Fatal("a refresh notified")
	}
	if err := store.LeaveSessionViewers(ctx, fixture.orgID, connection); err != nil {
		t.Fatal(err)
	}
	if !nextForSession(5 * time.Second) {
		t.Fatal("leave did not notify")
	}
}
