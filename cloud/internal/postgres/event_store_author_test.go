package postgres

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

func TestUserMessageEventPayloadOmitsAnAbsentAuthor(t *testing.T) {
	without, err := json.Marshal(userMessageEventPayload{chatMessagePayload: chatMessagePayload{Text: "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(without) != `{"text":"hello"}` {
		t.Fatalf("payload without author = %s", without)
	}
	with, err := json.Marshal(userMessageEventPayload{
		chatMessagePayload: chatMessagePayload{Text: "hello", ChatTurnSettings: domain.ChatTurnSettings{Model: "codex-test"}},
		AuthorUserID:       "user-1",
		AuthorName:         "Alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(with) != `{"text":"hello","model":"codex-test","authorUserId":"user-1","authorName":"Alice"}` {
		t.Fatalf("payload with author = %s", with)
	}
}

type userMessageAuthor struct {
	Text         string `json:"text"`
	AuthorUserID string `json:"authorUserId"`
	AuthorName   string `json:"authorName"`
}

func decodeUserMessageAuthor(t *testing.T, event domain.ClientEvent) userMessageAuthor {
	t.Helper()
	var payload userMessageAuthor
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("decode %s: %v", event.Payload, err)
	}
	return payload
}

// A shared session shows who wrote each prompt: the user message event names
// its sender, and an idempotent retry returns that same attributed event.
func TestSendMessageRecordsTheAuthor(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	key := uuid.NewString()

	event, err := store.SendMessage(ctx, principal, fixture.orgID, fixture.sessionID, key, "fix the build", domain.ChatTurnSettings{})
	if err != nil {
		t.Fatal(err)
	}
	got := decodeUserMessageAuthor(t, event)
	if got.Text != "fix the build" || got.AuthorUserID != fixture.userID || got.AuthorName != "Notification Test" {
		t.Fatalf("payload = %+v", got)
	}

	retried, err := store.SendMessage(ctx, principal, fixture.orgID, fixture.sessionID, key, "fix the build", domain.ChatTurnSettings{})
	if err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	if retried.Sequence != event.Sequence || decodeUserMessageAuthor(t, retried).AuthorUserID != fixture.userID {
		t.Fatalf("retry = %+v, want sequence %d with the same author", retried, event.Sequence)
	}

	events, _, err := store.ListClientEvents(ctx, principal, fixture.orgID, fixture.sessionID, 0, 100, []string{"chat.user_message"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || decodeUserMessageAuthor(t, events[0]).AuthorName != "Notification Test" {
		t.Fatalf("replayed events = %+v", events)
	}
}

// A message an orchestrator or automation sends has no human author.
func TestAutomatedMessageHasNoAuthor(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	ctx := context.Background()

	var event domain.ClientEvent
	err := store.withOrg(ctx, fixture.orgID, func(tx pgx.Tx) error {
		var err error
		event, err = sendMessageTx(ctx, tx, fixture.orgID, fixture.sessionID, "auto-"+uuid.NewString(), "CI failed",
			"", fixture.sessionID, "", nil, domain.ChatTurnSettings{}, false)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeUserMessageAuthor(t, event); got.AuthorUserID != "" || got.AuthorName != "" {
		t.Fatalf("automated message author = %+v", got)
	}
}
