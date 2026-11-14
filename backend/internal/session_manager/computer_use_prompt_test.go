package sessionmanager

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The computer-use section follows the setting at spawn time, and only chat
// sessions get it: their `ao mcp` server carries the tools. Off (or never
// wired) leaves the prompt as it was.
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
	sp, err := unwired.buildSystemPrompt(ctx, domain.KindWorker, "mer", "mer-1", "", true)
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
		for _, chat := range []bool{false, true} {
			enabled = false
			sp, err = m.buildSystemPrompt(ctx, kind, "mer", "mer-1", "", chat)
			if err != nil {
				t.Fatalf("buildSystemPrompt(%s, chat=%v): %v", kind, chat, err)
			}
			if strings.Contains(sp, "## Computer use") {
				t.Fatalf("%s chat=%v: computer use section present while off", kind, chat)
			}

			enabled = true
			sp, err = m.buildSystemPrompt(ctx, kind, "mer", "mer-1", "", chat)
			if err != nil {
				t.Fatalf("buildSystemPrompt(%s, chat=%v): %v", kind, chat, err)
			}
			if !chat {
				if strings.Contains(sp, "## Computer use") {
					t.Fatalf("%s: terminal session got the computer use section", kind)
				}
				continue
			}
			for _, want := range []string{"## Computer use", "Orchestrator's `ao` tool server", "`get_window_state`", "screenshot as an image", "commands/cua.md", "not available in this session", "Settings > Computer use agents", "untrusted", "which account the app or browser is signed in to", "use `ao browser`", "only when they explicitly ask", "`bring_to_front` takes keyboard focus", "Orchestrator refuses foreground input"} {
				if !strings.Contains(sp, want) {
					t.Fatalf("%s: chat prompt missing %q", kind, want)
				}
			}
			if strings.Contains(sp, "ao cua") {
				t.Fatalf("%s: chat prompt still mentions ao cua", kind)
			}
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
		if got := servers[0].Env[config.EnvComputerUse] == "1"; got != on {
			t.Fatalf("setting %v: server env = %v", on, servers[0].Env)
		}
	}
}

// Terminal sessions on macOS learn that macOS checks Orchestrator, not the
// agent's own app, so a refused permission sends the user to the right place.
// Chat sessions and other platforms do not get the section.
func TestBuildSystemPromptMacPrivacySection(t *testing.T) {
	ctx := context.Background()
	st := newFakeStore()
	m := New(Deps{
		Runtime:   &fakeRuntime{},
		Agents:    singleAgent{agent: &recordingAgent{}},
		Workspace: &fakeWorkspace{},
		Store:     st,
		Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st},
		DataDir:   t.TempDir(),
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})
	original := promptGOOS
	t.Cleanup(func() { promptGOOS = original })

	for _, goos := range []string{"darwin", "linux", "windows"} {
		promptGOOS = goos
		for _, kind := range []domain.SessionKind{domain.KindWorker, domain.KindOrchestrator} {
			for _, chat := range []bool{false, true} {
				sp, err := m.buildSystemPrompt(ctx, kind, "mer", "mer-1", "", chat)
				if err != nil {
					t.Fatalf("buildSystemPrompt(%s, %s, chat=%v): %v", goos, kind, chat, err)
				}
				has := strings.Contains(sp, "## macOS privacy permissions")
				if want := goos == "darwin" && !chat; has != want {
					t.Fatalf("%s %s chat=%v: section present=%v, want %v", goos, kind, chat, has, want)
				}
				if has {
					for _, want := range []string{"macOS checks Orchestrator's privacy permissions", "not the permissions of Claude", "Settings > Computer use agents > Orchestrator permissions", "even if the tool names another app", "restart"} {
						if !strings.Contains(sp, want) {
							t.Fatalf("%s: section missing %q", kind, want)
						}
					}
				}
			}
		}
	}
}
