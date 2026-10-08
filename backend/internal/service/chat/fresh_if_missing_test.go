package chat_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

// A driver may start fresh in place of a provider conversation that was never
// persisted, but only when the caller proved the conversation never started and
// the driver did not reattach to a live provider. The fresh conversation has no
// native history, so none is imported.
func TestResumeAcceptsFreshProviderOnlyWhenAllowed(t *testing.T) {
	for _, tc := range []struct {
		name           string
		freshIfMissing bool
		live           bool
		wantAccepted   bool
	}{
		{name: "allowed", freshIfMissing: true, wantAccepted: true},
		{name: "without proof", freshIfMissing: false},
		{name: "live provider", freshIfMissing: true, live: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := openStore(t)
			ctx := context.Background()
			now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
			record, found, err := st.GetSession(ctx, testSession)
			if err != nil || !found {
				t.Fatalf("GetSession: found=%v err=%v", found, err)
			}
			record.Metadata.ProviderConversationID = "reserved-provider-thread"
			if err := st.UpdateSession(ctx, record); err != nil {
				t.Fatalf("seed reserved provider handle: %v", err)
			}
			if _, err := st.CreateConversation(ctx, "fresh-if-missing-conversation",
				domain.ConversationScopeProject, testProject, testSession, now); err != nil {
				t.Fatalf("CreateConversation: %v", err)
			}

			// A history reader that refuses every replay: importing native history
			// from the fresh conversation would fail the start.
			fresh := &rejectedHistoryConversation{fakeConversation: newFakeConversation()}
			fresh.providerConversationID = "fresh-provider-thread"
			var returned ports.ChatConversation = fresh
			if tc.live {
				returned = &liveReconnectedConversation{&nativeHistoryConversation{fakeConversation: fresh.fakeConversation}}
			}
			var resumed ports.ChatResumeConfig
			svc := chatsvc.New(chatsvc.Options{
				Store: st, Sessions: st,
				Drivers: fakeRegistry{driver: fakeDriver{conv: returned, resumeCfg: &resumed}},
				Log:     slog.New(slog.DiscardHandler),
				NewID:   func() string { return "fresh-if-missing-generation" },
				Now:     func() time.Time { return now.Add(time.Second) },
			})
			t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })
			var ready string
			_, err = svc.Start(ctx, chatsvc.StartConfig{
				SessionID: testSession, ProjectID: testProject, Kind: domain.KindOrchestrator,
				Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
				ProviderConversationID:             "reserved-provider-thread",
				FreshIfProviderConversationMissing: tc.freshIfMissing,
				HistoryMode:                        ports.ChatHistoryImport,
				ControllerReady: func(result chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
					ready = result.ProviderConversationID
					return chatsvc.ControllerCommit{Conversation: result.Conversation}, nil
				},
			})

			if resumed.FreshIfMissing != tc.freshIfMissing {
				t.Fatalf("driver FreshIfMissing = %v, want %v", resumed.FreshIfMissing, tc.freshIfMissing)
			}
			if !tc.wantAccepted {
				if err == nil || !strings.Contains(err.Error(), "does not match requested handle") {
					t.Fatalf("Start error = %v, want provider handle mismatch", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			if ready != "fresh-provider-thread" {
				t.Fatalf("ControllerReady provider = %q, want the fresh provider", ready)
			}
		})
	}
}
