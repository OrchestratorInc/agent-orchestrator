package memcode

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNativeModelSelectionUsesConfirmedTUICommand(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p := &Plugin{}
	cfg := ports.LaunchConfig{WorkspacePath: t.TempDir(), Config: ports.AgentConfig{Model: "opus"}}
	command, err := p.AfterStartCommand(context.Background(), cfg)
	if err != nil || command != "/model opus" {
		t.Fatalf("command = %q, err = %v", command, err)
	}
	if p.AfterStartCommandApplied(cfg, readyTerminal) {
		t.Fatal("accepted idle composer without model confirmation")
	}
	if !p.AfterStartCommandApplied(cfg, readyTerminal+"\nmodel → opus") {
		t.Fatal("rejected native model confirmation")
	}
	for _, model := range []string{"bogus", "claude-opus-4-1"} {
		cfg.Config.Model = model
		if _, err := p.AfterStartCommand(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "catalog") {
			t.Fatalf("unsupported model %q: %v", model, err)
		}
	}
}
