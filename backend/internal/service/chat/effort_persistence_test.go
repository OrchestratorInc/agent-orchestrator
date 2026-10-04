package chat_test

import (
	"context"
	"log/slog"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

func TestChatEffortChangesPersistBeforeHandoff(t *testing.T) {
	for _, route := range []string{"turn settings", "config options"} {
		t.Run(route, func(t *testing.T) {
			ctx := context.Background()
			st := openStore(t)
			record, found, err := st.GetSession(ctx, testSession)
			if err != nil || !found {
				t.Fatalf("session: found=%v err=%v", found, err)
			}
			record.Harness = domain.HarnessClaudeCode
			if err := st.UpdateSession(ctx, record); err != nil {
				t.Fatal(err)
			}
			conv := &claudeDefaultsConversation{fakeConversation: newFakeConversation(), model: "default", effort: "default"}
			var picked []string
			svc := chatsvc.New(chatsvc.Options{
				Store: st, Sessions: st, Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
				Log: slog.New(slog.DiscardHandler), NewID: func() string { return "effort-persistence" },
				OnEffortChanged: func(id domain.SessionID, effort string) {
					if id != testSession {
						t.Errorf("session = %s", id)
					}
					picked = append(picked, effort)
				},
			})
			t.Cleanup(func() { _ = svc.Stop(ctx, testSession) })
			if _, err := svc.Start(ctx, chatsvc.StartConfig{SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessClaudeCode, WorkspacePath: t.TempDir()}); err != nil {
				t.Fatal(err)
			}
			set := func(effort string) {
				t.Helper()
				var err error
				if route == "config options" {
					_, err = svc.SetConfigOption(ctx, testSession, "effort", ports.ChatConfigOptionValue{Select: effort})
				} else {
					_, err = svc.SetTurnSettings(ctx, testSession, domain.ConversationSettings{ReasoningEffort: effort})
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			set("high")
			set("high")
			reset := ""
			if route == "config options" {
				reset = "default"
			}
			set(reset)
			if !reflect.DeepEqual(picked, []string{"high", ""}) {
				t.Fatalf("persisted efforts = %#v", picked)
			}
		})
	}
}
