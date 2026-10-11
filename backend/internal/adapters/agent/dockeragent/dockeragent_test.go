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
