package sessionmanager

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The computer-use section follows the setting at spawn time: on adds the
// ao cua pointer, off (or never wired) leaves the prompt as it was.
func TestBuildSystemPromptComputerUseFollowsSetting(t *testing.T) {
	ctx := context.Background()
	newManager := func() *Manager {
		st := newFakeStore()
		return New(Deps{
			Runtime:   &fakeRuntime{},
			Agents:    singleAgent{agent: &recordingAgent{}},
			Workspace: &fakeWorkspace{},
			Store:     st,
			Messenger: &fakeMessenger{},
			Lifecycle: &fakeLCM{store: st},
			DataDir:   t.TempDir(),
			LookPath:  func(string) (string, error) { return "/bin/true", nil },
		})
	}

	unwired := newManager()
	sp, err := unwired.buildSystemPrompt(ctx, domain.KindWorker, "mer", "mer-1", "", false)
	if err != nil {
		t.Fatalf("buildSystemPrompt: %v", err)
	}
	if strings.Contains(sp, "## Computer use") {
		t.Fatal("computer use section present without the setting")
	}

	enabled := false
	m := newManager()
	m.SetComputerUseEnabled(func(context.Context) bool { return enabled })
	for _, kind := range []domain.SessionKind{domain.KindWorker, domain.KindOrchestrator} {
		sp, err = m.buildSystemPrompt(ctx, kind, "mer", "mer-1", "", false)
		if err != nil {
			t.Fatalf("buildSystemPrompt(%s): %v", kind, err)
		}
		if strings.Contains(sp, "## Computer use") {
			t.Fatalf("%s: computer use section present while off", kind)
		}
	}

	enabled = true
	for _, kind := range []domain.SessionKind{domain.KindWorker, domain.KindOrchestrator} {
		sp, err = m.buildSystemPrompt(ctx, kind, "mer", "mer-1", "", false)
		if err != nil {
			t.Fatalf("buildSystemPrompt(%s): %v", kind, err)
		}
		for _, want := range []string{"## Computer use", "`ao cua guide`", "`ao cua call`", "never `cua-driver call`", "Settings > Computer use agents", "commands/cua.md", "untrusted"} {
			if !strings.Contains(sp, want) {
				t.Fatalf("%s: prompt missing %q", kind, want)
			}
		}
	}

	// A chat session gets the tools from its `ao mcp` server instead.
	sp, err = m.buildSystemPrompt(ctx, domain.KindWorker, "mer", "mer-1", "", true)
	if err != nil {
		t.Fatalf("buildSystemPrompt(chat): %v", err)
	}
	for _, want := range []string{"## Computer use", "AO's `ao` tool server", "`get_window_state`", "screenshot as an image", "commands/cua.md", "use `ao cua` in your shell", "untrusted"} {
		if !strings.Contains(sp, want) {
			t.Fatalf("chat prompt missing %q", want)
		}
	}
}

// The chat session's `ao mcp` server offers the computer-use tools only while
// the setting is on.
func TestAOMCPServerFollowsComputerUseSetting(t *testing.T) {
	enabled := false
	st := newFakeStore()
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, DataDir: t.TempDir(),
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	m.SetComputerUseEnabled(func(context.Context) bool { return enabled })
	env := map[string]string{EnvSessionID: "mer-1"}
	for _, on := range []bool{false, true} {
		enabled = on
		servers := m.aoMCPServers(context.Background(), domain.HarnessClaudeCode, env)
		if len(servers) != 1 {
			t.Fatalf("ao tool servers = %v", servers)
		}
		if got := servers[0].Env[envComputerUse] == "1"; got != on {
			t.Fatalf("setting %v: server env = %v", on, servers[0].Env)
		}
	}
}
