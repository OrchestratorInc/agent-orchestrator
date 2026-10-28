package postgres

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// The people in a shared session: its creator, then each person a link let
// in, in the order they joined, each with this session's role, whether they
// have it open, and whether they are writing to the agent or waiting to.
func TestSessionParticipantsListsWhoIsInASharedSession(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	owner := domain.Principal{UserID: fixture.userID, Provider: "local"}
	writer := shareSessionWith(t, store, admin, fixture, "editor", "Writer")
	waiter := shareSessionWith(t, store, admin, fixture, "editor", "Waiter")
	watcher := shareSessionWith(t, store, admin, fixture, "viewer", "Watcher")
	teammate := seedTestUser(t, admin, "Teammate")
	execOneRowAsOrg(t, admin, fixture.orgID, `INSERT INTO ao_org_memberships (org_id, user_id, role) VALUES ($1, $2, 'member')`, fixture.orgID, teammate.UserID)
	photos := map[string]string{
		owner.UserID:    "https://lh3.googleusercontent.com/owner",
		writer.UserID:   "https://lh3.googleusercontent.com/writer",
		teammate.UserID: "https://lh3.googleusercontent.com/teammate",
	}
	for userID, photo := range photos {
		if _, err := admin.Exec(ctx, `UPDATE ao_users SET avatar_url = $2 WHERE id = $1`, userID, photo); err != nil {
			t.Fatal(err)
		}
	}

	unshared := uuid.NewString()
	execOneRowAsOrg(t, admin, fixture.orgID, `INSERT INTO ao_sessions (id, org_id, project_id, kind, harness, display_name, branch, created_by_user_id)
		VALUES ($1, $2, $3, 'worker', 'codex', 'Private', 'private', $4)`, unshared, fixture.orgID, fixture.projectID, fixture.userID)

	for _, who := range []domain.Principal{writer, waiter, watcher, teammate} {
		if err := store.JoinSessionViewers(ctx, who, fixture.orgID, fixture.sessionID, uuid.NewString()); err != nil {
			t.Fatalf("join: %v", err)
		}
	}
	if _, err := store.AcquireComposerLease(ctx, writer, fixture.orgID, fixture.sessionID); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if _, err := store.RequestComposerTurn(ctx, waiter, fixture.orgID, fixture.sessionID); err != nil {
		t.Fatalf("queue: %v", err)
	}

	describe := func(participants []domain.SessionParticipant) string {
		out := ""
		for _, p := range participants {
			out += fmt.Sprintf("%s/%s/%v/%s ", p.Name, p.Role, p.Present, p.State)
		}
		return out
	}
	var ownerDisplayName string
	if err := admin.QueryRow(ctx, `SELECT COALESCE(display_name, '') FROM ao_users WHERE id = $1`, fixture.userID).Scan(&ownerDisplayName); err != nil {
		t.Fatalf("owner name: %v", err)
	}
	want := fmt.Sprintf("%s/owner/false/idle Writer/editor/true/working Waiter/editor/true/waiting Watcher/viewer/true/idle Teammate/member/true/idle ", ownerDisplayName)
	for _, reader := range []domain.Principal{owner, watcher} {
		got, err := store.SessionParticipants(ctx, reader, fixture.orgID, []string{fixture.sessionID, unshared})
		if err != nil {
			t.Fatalf("participants as %s: %v", reader.UserID, err)
		}
		if line := describe(got[fixture.sessionID]); line != want {
			t.Fatalf("participants as %s:\n got %q\nwant %q", reader.UserID, line, want)
		}
		for _, participant := range got[fixture.sessionID] {
			if participant.AvatarURL != photos[participant.UserID] {
				t.Fatalf("photo for %s = %q, want %q", participant.Name, participant.AvatarURL, photos[participant.UserID])
			}
		}
		if _, listed := got[unshared]; listed {
			t.Fatalf("a session nobody shared is listed for %s", reader.UserID)
		}
	}

	stranger := seedTestUser(t, admin, "Stranger")
	got, err := store.SessionParticipants(ctx, stranger, fixture.orgID, []string{fixture.sessionID})
	if err != nil || len(got) != 0 {
		t.Fatalf("a stranger sees %v, %v; want nothing", got, err)
	}
}
