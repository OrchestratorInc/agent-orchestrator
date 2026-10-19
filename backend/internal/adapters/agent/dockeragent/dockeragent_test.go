package dockeragent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestRejectsTerminalEvenWithTaskAndNativeIdentity(t *testing.T) {
	p := New()
	if p.Manifest().ID != "docker-agent" {
		t.Fatal(p.Manifest())
	}
	cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{Prompt: "-task", NativeSessionID: "native"})
	if err == nil || !strings.Contains(err.Error(), "Chat mode only") || len(cmd) != 0 {
		t.Fatalf("cmd=%v err=%v", cmd, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.GetLaunchCommand(ctx, ports.LaunchConfig{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, ok := any(p).(ports.AgentInterfaceHandoff); ok {
		t.Fatal("unproven interface handoff")
	}
}

func TestDefaultConfigUsesAvailableProviders(t *testing.T) {
	for _, key := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GOOGLE_API_KEY", "GEMINI_API_KEY"} {
		t.Setenv(key, "")
	}
	if _, err := DefaultConfig(nil); err == nil {
		t.Fatal("default config without a provider key")
	}
	b, err := DefaultConfig(map[string]string{"OPENAI_API_KEY": "key", "GEMINI_API_KEY": "key"})
	if err != nil {
		t.Fatal(err)
	}
	config := string(b)
	if !strings.Contains(config, "root:\n    model: gpt-5.6\n") || !strings.Contains(config, "provider: google") || strings.Contains(config, "anthropic") {
		t.Fatalf("unexpected default config:\n%s", config)
	}
}
