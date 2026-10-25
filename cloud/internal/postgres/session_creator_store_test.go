package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// A session acts on GitHub as its creator: the store names them for a live
// session of the organization only.
func TestSessionCreatorUserIDNamesTheLiveSessionsCreator(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	execOneRowAsOrg(t, admin, fixture.orgID, `UPDATE ao_sessions SET created_by_user_id = NULL WHERE id = $1`, fixture.sessionID)
	if _, err := store.SessionCreatorUserID(ctx, fixture.orgID, fixture.sessionID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session without a creator: err = %v, want ErrNotFound", err)
	}

	execOneRowAsOrg(t, admin, fixture.orgID, `UPDATE ao_sessions SET created_by_user_id = $1 WHERE id = $2`, fixture.userID, fixture.sessionID)
	creator, err := store.SessionCreatorUserID(ctx, fixture.orgID, fixture.sessionID)
	if err != nil || creator != fixture.userID {
		t.Fatalf("creator = %q, %v; want %q", creator, err, fixture.userID)
	}

	// Another organization cannot see the session (row-level security).
	if _, err := store.SessionCreatorUserID(ctx, uuid.NewString(), fixture.sessionID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another organization: err = %v, want ErrNotFound", err)
	}

	execOneRowAsOrg(t, admin, fixture.orgID, `UPDATE ao_sessions SET is_terminated = true WHERE id = $1`, fixture.sessionID)
	if _, err := store.SessionCreatorUserID(ctx, fixture.orgID, fixture.sessionID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("terminated session: err = %v, want ErrNotFound", err)
	}
}
