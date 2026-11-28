package codex

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/skillbridge"
)

func TestSkillSupport(t *testing.T) {
	home, codexHome := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	plugin := &Plugin{}
	chat := plugin.SkillSupport(context.Background(), ports.AgentSkillRequest{Chat: true})
	if chat.Layout != skillbridge.AgentsLayout {
		t.Fatalf("chat layout = %q", chat.Layout)
	}
	want := []string{filepath.Join(home, ".codex", "skills"), filepath.Join(home, ".agents", "skills")}
	if !slices.Equal(chat.Native, want) {
		t.Fatalf("native = %v, want %v", chat.Native, want)
	}
	terminal := plugin.SkillSupport(context.Background(), ports.AgentSkillRequest{Env: map[string]string{"CODEX_HOME": codexHome}})
	if terminal.Layout != "" || terminal.Native[0] != filepath.Join(codexHome, "skills") {
		t.Fatalf("terminal = %+v", terminal)
	}
	if roots := SkillRoots(t.TempDir(), "s-1"); roots != nil {
		t.Fatalf("roots without skills = %v", roots)
	}
}
