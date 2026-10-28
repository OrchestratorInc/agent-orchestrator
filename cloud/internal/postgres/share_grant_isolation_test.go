package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// A person holding a trusted editor link to one session and a view-only link
// to another session of the same project gets each grant only where it
// applies, whichever link they opened first.
func TestAViewOnlyLinkStaysViewOnlyBesideAnotherSessionsEditorLink(t *testing.T) {
	for _, viewOnlyFirst := range []bool{false, true} {
		store, admin, fixture := openNotificationTestStore(t)
		ctx := context.Background()
		owner := domain.Principal{UserID: fixture.userID, Provider: "local"}
		trustedSession := fixture.sessionID
		viewOnlySession := uuid.NewString()
		execOneRowAsOrg(t, admin, fixture.orgID, `INSERT INTO ao_sessions (id, org_id, project_id, kind, harness, display_name, branch, created_by_user_id)
			VALUES ($1, $2, $3, 'worker', 'codex', 'View only', 'view-only', $4)`, viewOnlySession, fixture.orgID, fixture.projectID, fixture.userID)

		guest := seedTestUser(t, admin, "Guest")
		t.Cleanup(func() {
			execAsOrg(admin, fixture.orgID, `DELETE FROM ao_project_share_grants WHERE org_id = $1 AND user_id = $2`, fixture.orgID, guest.UserID)
		})
		redeem := func(link domain.CreateShareLink) {
			t.Helper()
			_, token, err := store.CreateProjectShareLink(ctx, owner, fixture.orgID, fixture.projectID, link)
			if err != nil {
				t.Fatalf("create link: %v", err)
			}
			if _, err := store.RedeemProjectShareLink(ctx, guest, fixture.orgID, token); err != nil {
				t.Fatalf("redeem link: %v", err)
			}
		}
		trusted := domain.CreateShareLink{SessionID: trustedSession, Role: "editor", AccessScope: "anyone", Interaction: "interact", ModeCap: "trusted"}
		viewOnly := domain.CreateShareLink{SessionID: viewOnlySession, Role: "viewer", AccessScope: "anyone", Interaction: "view", ModeCap: "read-only"}
		if viewOnlyFirst {
			redeem(viewOnly)
			redeem(trusted)
		} else {
			redeem(trusted)
			redeem(viewOnly)
		}

		roleIn := func(sessionID string) sessionAccess {
			t.Helper()
			var got sessionAccess
			if err := store.withSessionAccess(ctx, guest, fixture.orgID, sessionID, func(_ pgx.Tx, access sessionAccess) error {
				got = access
				return nil
			}); err != nil {
				t.Fatalf("session access: %v", err)
			}
			return got
		}
		if got := roleIn(viewOnlySession); got.Role != "viewer" || got.ModeCap != "read-only" {
			t.Fatalf("view-only first=%v: the view-only session gives %s/%s, want viewer/read-only", viewOnlyFirst, got.Role, got.ModeCap)
		}
		if got := roleIn(trustedSession); got.Role != "editor" || got.ModeCap != "trusted" {
			t.Fatalf("view-only first=%v: the trusted session gives %s/%s, want editor/trusted", viewOnlyFirst, got.Role, got.ModeCap)
		}
		if _, err := store.SendMessage(ctx, guest, fixture.orgID, viewOnlySession, uuid.NewString(), "hi", domain.ChatTurnSettings{}); !errors.Is(err, ErrForbidden) {
			t.Fatalf("view-only first=%v: a message in the view-only session: %v, want ErrForbidden", viewOnlyFirst, err)
		}
		if _, err := store.AcquireComposerLease(ctx, guest, fixture.orgID, viewOnlySession); !errors.Is(err, ErrForbidden) {
			t.Fatalf("view-only first=%v: the chat box in the view-only session: %v, want ErrForbidden", viewOnlyFirst, err)
		}
		if _, err := store.CreateWorkspaceRequest(ctx, guest, fixture.orgID, viewOnlySession, "workspace.checkout", []byte(`{}`), time.Minute); !errors.Is(err, ErrForbidden) {
			t.Fatalf("view-only first=%v: Retry in the view-only session: %v, want ErrForbidden", viewOnlyFirst, err)
		}
		if _, err := store.CreateWorkspaceRequest(ctx, guest, fixture.orgID, viewOnlySession, "workspace.read", []byte(`{"path":"README.md"}`), time.Minute); errors.Is(err, ErrForbidden) {
			t.Fatalf("view-only first=%v: reading a file in the view-only session was refused", viewOnlyFirst)
		}
		if _, err := store.AcquireComposerLease(ctx, guest, fixture.orgID, trustedSession); err != nil {
			t.Fatalf("view-only first=%v: the chat box in the trusted session: %v", viewOnlyFirst, err)
		}
	}
}
