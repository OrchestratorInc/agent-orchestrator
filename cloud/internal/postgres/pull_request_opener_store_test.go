package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/google/uuid"
)

func TestSessionPullRequestOpenerNamesTheSessionsPerson(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	if _, err := admin.Exec(ctx,
		`UPDATE ao_users SET display_name = '', email = 'ada@example.com', avatar_url = 'https://ao.example/ada.png' WHERE id = $1`,
		fixture.userID,
	); err != nil {
		t.Fatal(err)
	}
	execOneRowAsOrg(t, admin, fixture.orgID, `UPDATE ao_sessions SET created_by_user_id = $1 WHERE id = $2`, fixture.userID, fixture.sessionID)
	member := domain.Principal{UserID: fixture.userID, Provider: "local"}
	opener, err := store.SessionPullRequestOpener(ctx, member, fixture.orgID, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	// No display name and no GitHub login: nothing names them, not even their email.
	if opener.Name != "" || opener.GitHubLogin != "" {
		t.Fatalf("opener without a name or GitHub = %+v, want neither", opener)
	}

	if _, err := admin.Exec(ctx, `UPDATE ao_users SET display_name = 'Ada Lovelace' WHERE id = $1`, fixture.userID); err != nil {
		t.Fatal(err)
	}
	// A person's GitHub connection is theirs alone (forced row-level security
	// on ao.user_id), so it is written as them.
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('ao.user_id', $1, true)`, fixture.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO ao_github_user_connections (
			user_id, github_user_id, github_login, github_avatar_url, access_token_ciphertext, access_token_nonce
		) VALUES ($1, 4242, 'ada-gh', 'https://avatars.example/ada', '\x01'::bytea, '\x000000000000000000000000'::bytea)`,
		fixture.userID,
	); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	opener, err = store.SessionPullRequestOpener(ctx, member, fixture.orgID, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if opener.Name != "Ada Lovelace" || opener.GitHubLogin != "ada-gh" || opener.AvatarURL != "https://avatars.example/ada" {
		t.Fatalf("opener with GitHub = %+v", opener)
	}

	// Someone outside the organization (a project share grantee) is refused.
	outsider := uuid.NewString()
	if _, err := admin.Exec(ctx, `INSERT INTO ao_users (
		id, auth_provider, external_user_id, email, display_name, password_hash
	) VALUES ($1::uuid, 'local', $1::uuid::text, $1::uuid::text || '@example.test', 'Outsider', 'hash')`, outsider); err != nil {
		t.Fatal(err)
	}
	_, err = store.SessionPullRequestOpener(ctx, domain.Principal{UserID: outsider, Provider: "local"}, fixture.orgID, fixture.sessionID)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("outsider error = %v, want ErrForbidden", err)
	}
}
