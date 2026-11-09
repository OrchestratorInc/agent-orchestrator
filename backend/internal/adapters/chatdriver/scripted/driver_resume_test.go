package scripted_test

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/scripted"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestResumePreservesConversationIdentity 删除创建时注入 ID 的逻辑会让真实 Resume 返回新会话身份。
func TestResumePreservesConversationIdentity(t *testing.T) {
	for _, steerable := range []bool{false, true} {
		driver := scripted.NewDriver(scripted.Config{Harness: domain.HarnessFake, Steerable: steerable})
		conversation, err := driver.Resume(context.Background(), ports.ChatResumeConfig{ProviderConversationID: "existing-thread"})
		if err != nil {
			t.Fatal(err)
		}
		if got := conversation.ProviderConversationID(); got != "existing-thread" {
			t.Fatalf("steerable=%v resumed identity=%q", steerable, got)
		}
		if got := driver.Conversations()[0].ProviderConversationID(); got != "existing-thread" {
			t.Fatalf("recorded identity=%q", got)
		}
		if _, ok := conversation.(ports.ChatSteerer); ok != steerable {
			t.Fatalf("steerable=%v wrapper=%v", steerable, ok)
		}
	}
}
