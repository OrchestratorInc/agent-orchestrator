package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

func sentSettings(t *testing.T, event domain.ClientEvent) domain.ChatTurnSettings {
	t.Helper()
	var settings domain.ChatTurnSettings
	if err := json.Unmarshal(event.Payload, &settings); err != nil {
		t.Fatal(err)
	}
	return settings
}

// The session's owner sets the model and its settings; everyone in the
// session sees them, and only the owner may change them.
func TestSessionChatSettingsBelongToTheOwner(t *testing.T) {
	c := openComposerFixture(t)
	ctx := context.Background()
	chosen := domain.ChatTurnSettings{Model: "opus", ReasoningEffort: "high", ApprovalMode: "accept-edits"}
	if _, err := c.store.SetSessionChatSettings(ctx, c.owner, c.fixture.orgID, c.fixture.sessionID, chosen); err != nil {
		t.Fatalf("the owner setting the model: %v", err)
	}
	for _, who := range []domain.Principal{c.alice, c.viewer} {
		if _, err := c.store.SetSessionChatSettings(ctx, who, c.fixture.orgID, c.fixture.sessionID, domain.ChatTurnSettings{Model: "haiku"}); !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s changing the model: %v, want ErrForbidden", who.DisplayName, err)
		}
	}
	for who, canConfigure := range map[*domain.Principal]bool{&c.owner: true, &c.alice: false, &c.viewer: false} {
		state, err := c.store.SessionLiveState(ctx, *who, c.fixture.orgID, c.fixture.sessionID)
		if err != nil {
			t.Fatal(err)
		}
		if state.ChatSettings != chosen || state.CanConfigure != canConfigure {
			t.Fatalf("%s sees settings %+v, canConfigure %v", who.UserID, state.ChatSettings, state.CanConfigure)
		}
	}
}

// A guest's message runs with the owner's settings whatever it asks for,
// and with none while the owner has set none; the owner's own message runs
// with the owner's choice.
func TestGuestMessagesRunWithTheOwnersSettings(t *testing.T) {
	c := openComposerFixture(t)
	ctx := context.Background()
	send := func(who domain.Principal, settings domain.ChatTurnSettings) domain.ChatTurnSettings {
		t.Helper()
		event, err := c.store.SendMessageWithLease(ctx, who, c.fixture.orgID, c.fixture.sessionID, uuid.NewString(), "hi", settings, 0)
		if err != nil {
			t.Fatalf("send by %s: %v", who.DisplayName, err)
		}
		return sentSettings(t, event)
	}
	if got := send(c.alice, domain.ChatTurnSettings{Model: "expensive"}); got != (domain.ChatTurnSettings{}) {
		t.Fatalf("a guest's message before the owner chose ran with %+v", got)
	}
	chosen := domain.ChatTurnSettings{Model: "sonnet", ReasoningEffort: "medium"}
	if _, err := c.store.SetSessionChatSettings(ctx, c.owner, c.fixture.orgID, c.fixture.sessionID, chosen); err != nil {
		t.Fatal(err)
	}
	if got := send(c.alice, domain.ChatTurnSettings{Model: "expensive", ReasoningEffort: "max"}); got != chosen {
		t.Fatalf("a guest's message ran with %+v, want the owner's %+v", got, chosen)
	}
	if got := send(c.owner, domain.ChatTurnSettings{Model: "opus"}); got.Model != "opus" {
		t.Fatalf("the owner's message ran with %+v", got)
	}
}

// A teammate in the organization keeps their own choice until the owner
// sets the session's, then runs with the owner's.
// A guest retrying a message the server ran with the owner's settings gets
// the same message back, even after the owner changed them in between.
func TestGuestRetryMatchesTheMessageItSent(t *testing.T) {
	c := openComposerFixture(t)
	ctx := context.Background()
	if _, err := c.store.SetSessionChatSettings(ctx, c.owner, c.fixture.orgID, c.fixture.sessionID, domain.ChatTurnSettings{Model: "opus"}); err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	asked := domain.ChatTurnSettings{Model: "haiku", ReasoningEffort: "low"}
	first, err := c.store.SendMessageWithLease(ctx, c.alice, c.fixture.orgID, c.fixture.sessionID, key, "hi", asked, 0)
	if err != nil {
		t.Fatalf("first send: %v", err)
	}
	if got := sentSettings(t, first); got.Model != "opus" {
		t.Fatalf("the message ran with %+v, want the owner's opus", got)
	}
	if _, err := c.store.SetSessionChatSettings(ctx, c.owner, c.fixture.orgID, c.fixture.sessionID, domain.ChatTurnSettings{Model: "sonnet"}); err != nil {
		t.Fatal(err)
	}
	retried, err := c.store.SendMessageWithLease(ctx, c.alice, c.fixture.orgID, c.fixture.sessionID, key, "hi", asked, 0)
	if err != nil {
		t.Fatalf("an identical retry: %v", err)
	}
	if retried.Sequence != first.Sequence {
		t.Fatalf("the retry returned event %d, want the first message %d", retried.Sequence, first.Sequence)
	}
	if _, err := c.store.SendMessageWithLease(ctx, c.alice, c.fixture.orgID, c.fixture.sessionID, key, "different text", asked, 0); err == nil {
		t.Fatal("a different message under the same key was accepted")
	}
}

func TestTeammateMessagesFollowTheOwnerOnceSet(t *testing.T) {
	c := openComposerFixture(t)
	ctx := context.Background()
	teammate := seedTestUser(t, c.admin, "Teammate")
	execOneRowAsOrg(t, c.admin, c.fixture.orgID, `INSERT INTO ao_org_memberships (org_id, user_id, role) VALUES ($1, $2, 'member')`, c.fixture.orgID, teammate.UserID)
	t.Cleanup(func() {
		execAsOrg(c.admin, c.fixture.orgID, `DELETE FROM ao_org_memberships WHERE org_id = $1 AND user_id = $2`, c.fixture.orgID, teammate.UserID)
	})
	event, err := c.store.SendMessageWithLease(ctx, teammate, c.fixture.orgID, c.fixture.sessionID, uuid.NewString(), "hi", domain.ChatTurnSettings{Model: "haiku"}, 0)
	if err != nil || sentSettings(t, event).Model != "haiku" {
		t.Fatalf("a teammate's own choice before the owner set one: %+v, %v", event, err)
	}
	if _, err := c.store.SetSessionChatSettings(ctx, c.owner, c.fixture.orgID, c.fixture.sessionID, domain.ChatTurnSettings{Model: "opus"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.SetSessionChatSettings(ctx, teammate, c.fixture.orgID, c.fixture.sessionID, domain.ChatTurnSettings{Model: "haiku"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a teammate changing the owner's model: %v", err)
	}
	event, err = c.store.SendMessageWithLease(ctx, teammate, c.fixture.orgID, c.fixture.sessionID, uuid.NewString(), "hi", domain.ChatTurnSettings{Model: "haiku"}, 0)
	if err != nil || sentSettings(t, event).Model != "opus" {
		t.Fatalf("a teammate's message after the owner chose: %+v, %v", event, err)
	}
}

// Changing the settings wakes the session's live streams.
func TestSessionChatSettingsNotifyLiveListeners(t *testing.T) {
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
	if _, err := c.store.SetSessionChatSettings(ctx, c.owner, c.fixture.orgID, c.fixture.sessionID, domain.ChatTurnSettings{Model: "opus"}); err != nil {
		t.Fatal(err)
	}
	for {
		notification, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			t.Fatalf("no notification for the settings change: %v", err)
		}
		if notification.Payload == c.fixture.sessionID {
			return
		}
	}
}

// A session whose creator is gone (older sessions, or a deleted account)
// still works for everyone allowed in: nobody is its creator, so only the
// organization's owners and admins manage it.
func TestSessionWithoutACreatorStillWorks(t *testing.T) {
	c := openComposerFixture(t)
	ctx := context.Background()
	execOneRowAsOrg(t, c.admin, c.fixture.orgID, `UPDATE ao_sessions SET created_by_user_id = NULL WHERE org_id = $1 AND id = $2`, c.fixture.orgID, c.fixture.sessionID)

	for _, who := range []domain.Principal{c.owner, c.alice, c.viewer} {
		state, err := c.store.SessionLiveState(ctx, who, c.fixture.orgID, c.fixture.sessionID)
		if err != nil {
			t.Fatalf("live state for %s: %v", who.UserID, err)
		}
		wantManage := who.UserID == c.owner.UserID
		if state.CanTakeOver != wantManage || state.CanConfigure != wantManage {
			t.Fatalf("permissions for %s without a session creator: %+v, want manage=%t", who.DisplayName, state, wantManage)
		}
	}
	if _, err := c.store.SendMessageWithLease(ctx, c.alice, c.fixture.orgID, c.fixture.sessionID, uuid.NewString(), "hi", domain.ChatTurnSettings{}, 0); err != nil {
		t.Fatalf("a guest's message: %v", err)
	}
	if _, err := c.store.AcquireComposerLease(ctx, c.alice, c.fixture.orgID, c.fixture.sessionID); err != nil {
		t.Fatalf("the chat box: %v", err)
	}
	if _, err := c.store.SessionParticipants(ctx, c.alice, c.fixture.orgID, []string{c.fixture.sessionID}); err != nil {
		t.Fatalf("participants: %v", err)
	}
}
