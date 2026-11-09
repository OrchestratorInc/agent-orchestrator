package chat_test

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

// TestProfileBackgroundPreservesEffortPolicy 删除固定选择分支会在启动或首轮把 high/default 降为目录中的 low。
func TestProfileBackgroundPreservesEffortPolicy(t *testing.T) {
	for _, effort := range []string{"high", ""} {
		t.Run("effort="+effort, func(t *testing.T) {
			conversation := &backgroundModelConversation{
				backgroundTestConversation: &backgroundTestConversation{terminatingConversation: &terminatingConversation{fakeConversation: newFakeConversation()}},
				models:                     []ports.ChatModel{{ID: "fixed-model", Default: true, Efforts: []string{"low", "high"}}},
			}
			var started ports.ChatStartConfig
			driver := &suggestionForkDriver{fakeDriver: fakeDriver{conv: conversation, startCfg: &started}}
			driver.fork = func(context.Context, string, string, ports.ChatStartConfig) (ports.ChatConversation, error) {
				t.Fatal("fixed profile used native fork")
				return nil, nil
			}
			service := chatsvc.New(chatsvc.Options{Drivers: fakeRegistry{driver: driver}, DataDir: t.TempDir(), AppRunID: "profile-background", NewID: func() string { return "id" }})
			t.Cleanup(func() { service.StopAll(context.Background()) })
			cfg := ports.ChatStartConfig{Profile: "fixed-profile", Model: "fixed-model", Effort: effort}
			if _, err := service.RunPromptSuggestionTask(context.Background(), "session", "turn", domain.HarnessCodex, cfg, "Write a title"); err != nil {
				t.Fatal(err)
			}
			message := conversation.sentMessages()[0]
			if started.Profile != cfg.Profile || started.Effort != effort || message.Settings.Model != cfg.Model || message.Settings.Effort != effort {
				t.Fatalf("fixed selection lost: start=%+v turn=%+v", started, message.Settings)
			}
		})
	}
}
