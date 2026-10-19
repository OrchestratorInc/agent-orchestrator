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

func TestNativeModelConfirmationRequiresExactNewestToken(t *testing.T) {
	p := New()
	cfg := ports.LaunchConfig{Config: ports.AgentConfig{Model: "glm-5p3"}}
	for _, test := range []struct {
		confirmation string
		want         bool
	}{
		{"model → glm-5p3", true},
		{"model → glm-5p3-flash", false},
		{"model → glm-5p3\nmodel → glm-5p3-flash", false},
		{"model → glm-5p3-flash\nmodel → glm-5p3", true},
		{"model → glm-5p3 · 200k context", true},
	} {
		if got := p.AfterStartCommandApplied(cfg, readyTerminal+"\n"+test.confirmation); got != test.want {
			t.Errorf("confirmation %q = %v, want %v", test.confirmation, got, test.want)
		}
	}
}
