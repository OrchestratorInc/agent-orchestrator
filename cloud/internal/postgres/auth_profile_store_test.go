package postgres

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/google/uuid"
)

func TestWorkOSSignInRefreshesTheProfilePhoto(t *testing.T) {
	_, admin, _ := openNotificationTestStore(t)
	store, _ := openAppRoleStore(t, admin)
	ctx := context.Background()
	externalID := "user_" + uuid.NewString()
	principal := domain.Principal{Provider: "workos", ExternalID: externalID, Email: externalID + "@example.test", DisplayName: "Ada"}
	var userID string
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM ao_organizations WHERE owner_user_id = $1`, userID)
		_, _ = admin.Exec(context.Background(), `DELETE FROM ao_users WHERE id = $1`, userID)
	})
	for _, photo := range []string{"https://lh3.googleusercontent.com/first", "https://lh3.googleusercontent.com/updated", ""} {
		principal.AvatarURL = photo
		signedIn, err := store.UpsertWorkOSUser(ctx, principal)
		if err != nil {
			t.Fatal(err)
		}
		if userID != "" && userID != signedIn.UserID {
			t.Fatal("refreshing the photo changed the account")
		}
		userID = signedIn.UserID
		var stored string
		if err := admin.QueryRow(ctx, `SELECT avatar_url FROM ao_users WHERE id = $1`, userID).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if stored != photo {
			t.Fatalf("stored photo = %q, want %q", stored, photo)
		}
	}
}
