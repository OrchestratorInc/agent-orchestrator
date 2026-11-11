package daemon

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type usageTestIdentity struct {
	optedOut   bool
	suppressed bool
}

func (i *usageTestIdentity) Snapshot() ports.TelemetryIdentity {
	return ports.TelemetryIdentity{InstallID: "install-one", OptedOut: i.optedOut || i.suppressed}
}
func (i *usageTestIdentity) OptedOut() bool                                 { return i.optedOut }
func (i *usageTestIdentity) ExportSuppressed() bool                         { return i.optedOut || i.suppressed }
func (*usageTestIdentity) SetCloudUserID(context.Context, string) error     { return nil }
func (*usageTestIdentity) MarkInstalledEvent(context.Context) (bool, error) { return false, nil }
func (*usageTestIdentity) SetGitHubLogin(string)                            {}

type usageTestSink struct{ events []ports.TelemetryEvent }

func (s *usageTestSink) Emit(_ context.Context, e ports.TelemetryEvent) {
	s.events = append(s.events, e)
}
func (*usageTestSink) Close(context.Context) error { return nil }

func TestUsageFlusherEmitsOneContentFreeBatchAndDiscardsOptedOutWork(t *testing.T) {
	store := sqlitetest.MustOpen(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "private-project", Path: "/private/repo", RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "private-project", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Activity:  domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := store.CreateConversation(ctx, "usage-conversation", domain.ConversationScopeSession, "private-project", session.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	identity := &usageTestIdentity{}
	store.SetUsageTelemetryAllowed(func() bool { return !identity.optedOut })
	add := func(id string) {
		t.Helper()
		created, err := store.AppendUserMessage(ctx, conversation.ID, session.ID, "generation",
			domain.ConversationMessage{ID: id, Text: "private content", Origin: domain.MessageOriginHuman,
				ClientMessageID: id}, "turn-"+id, now)
		if err != nil || !created {
			t.Fatalf("append: created=%v err=%v", created, err)
		}
	}
	add("one")
	add("two")
	sink := &usageTestSink{}
	f := &usageTelemetryFlusher{store: store, sink: sink, identity: identity, log: slog.Default()}
	f.flush(ctx, false)
	if len(sink.events) != 1 {
		t.Fatalf("events = %+v, want one batch", sink.events)
	}
	e := sink.events[0]
	if e.Name != "ao.usage.messages_sent" || e.Payload["count"] != int64(2) ||
		e.ProjectID != nil || e.SessionID != nil {
		t.Fatalf("event = %+v", e)
	}
	if e.Payload["project_telemetry_id"] == "private-project" {
		t.Fatal("local project ID exported")
	}
	for _, key := range []string{"text", "url", "repo", "path"} {
		if _, ok := e.Payload[key]; ok {
			t.Fatalf("payload leaked %s", key)
		}
	}
	add("pending")
	identity.optedOut = true
	f.flush(ctx, false)
	add("while-off")
	identity.optedOut = false
	f.flush(ctx, false)
	if len(sink.events) != 1 {
		t.Fatalf("replayed messages after opt-out: %+v", sink.events)
	}
	identity.suppressed = true
	add("identity-write-failed")
	f.flush(ctx, false)
	if len(sink.events) != 1 {
		t.Fatalf("exported while identity persistence failed: %+v", sink.events)
	}
	identity.suppressed = false
	f.flush(ctx, false)
	if len(sink.events) != 2 || sink.events[1].Payload["count"] != int64(1) {
		t.Fatalf("pending count after identity recovered = %+v", sink.events)
	}
}
