package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/skillbridge"
)

// Claude Code's terminal stops on a trust question for a directory given with
// --add-dir, so a terminal launch must never carry one.
func TestTerminalCommandsNeverAddADirectory(t *testing.T) {
	data, source := t.TempDir(), t.TempDir()
	skill := filepath.Join(source, "mine", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("---\nname: mine\ndescription: d\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Left from when the session ran as a chat.
	if _, err := skillbridge.Sync(context.Background(), skillbridge.Config{
		Dir: skillbridge.SessionDir(data, "claude-code", "s-1"), Layout: skillbridge.ClaudeLayout, Sources: []string{source},
	}); err != nil {
		t.Fatal(err)
	}
	if len(SkillDirectories(data, "s-1")) != 1 || SkillDirectories(data, "s-2") != nil {
		t.Fatal("the chat session's folder is not found")
	}
	plugin := &Plugin{resolvedBinary: "claude"}
	launch, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{DataDir: data, SessionID: "s-1", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	restore, ok, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		DataDir: data, Session: ports.SessionRef{
			ID: "s-1", Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "6f9619ff-8b86-4d01-b42d-00cf4fc964ff"},
		},
	})
	if err != nil || !ok {
		t.Fatalf("restore: %v, %v", ok, err)
	}
	if slices.Contains(launch, "--add-dir") || slices.Contains(restore, "--add-dir") {
		t.Fatalf("launch %v, restore %v", launch, restore)
	}
	if got := plugin.SkillSupport(context.Background(), ports.AgentSkillRequest{}); got.Layout != "" {
		t.Fatalf("terminal layout = %q", got.Layout)
	}
}

func TestSkillSupportFollowsTheConfigFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	plugin := &Plugin{}
	if got := plugin.SkillSupport(context.Background(), ports.AgentSkillRequest{Chat: true}); got.Layout != skillbridge.ClaudeLayout ||
		!slices.Equal(got.Native, []string{filepath.Join(home, ".claude", "skills")}) {
		t.Fatalf("support = %+v", got)
	}
	config := t.TempDir()
	got := plugin.SkillSupport(context.Background(), ports.AgentSkillRequest{Env: map[string]string{"CLAUDE_CONFIG_DIR": config}})
	if !slices.Equal(got.Native, []string{filepath.Join(config, "skills")}) {
		t.Fatalf("support = %+v", got)
	}
}
