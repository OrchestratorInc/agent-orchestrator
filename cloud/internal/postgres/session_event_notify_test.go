package postgres

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// Every append to a session's event log, from a user message or any other
// path, notifies ao_session_event with that session's id once committed.
func TestEventAppendNotifiesSessionEventListeners(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := admin.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `LISTEN ao_session_event`); err != nil {
		t.Fatal(err)
	}
	// Other tests share the database: wait for this session's notification.
	waitFor := func(what string) {
		t.Helper()
		for {
			notification, err := conn.Conn().WaitForNotification(ctx)
			if err != nil {
				t.Fatalf("%s: no notification: %v", what, err)
			}
			if notification.Payload == fixture.sessionID {
				return
			}
		}
	}

	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	if _, err := store.SendMessage(ctx, principal, fixture.orgID, fixture.sessionID, uuid.NewString(), "hello", domain.ChatTurnSettings{}); err != nil {
		t.Fatal(err)
	}
	waitFor("user message")

	if _, err := store.AppendSessionEvent(ctx, fixture.orgID, fixture.sessionID, "workspace.changed", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	waitFor("session event")
}
