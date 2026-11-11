package acp

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestInitializeOmitsHostIdentity(t *testing.T) {
	agent := &fakeAgent{}
	driver := New(Config{
		Harness: domain.HarnessClaudeCode,
		Launch: func(context.Context, LaunchConfig) (Launch, error) {
			return Launch{Command: "fake"}, nil
		},
	}, nil)
	driver.useTestProcess(fakeSpawn(agent))
	conversation, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer conversation.Close()

	agent.mu.Lock()
	defer agent.mu.Unlock()
	if agent.initParams.ClientInfo != nil {
		t.Fatalf("initialize 泄露了宿主身份: %+v", agent.initParams.ClientInfo)
	}
}
