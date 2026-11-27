package sessionmanager

import (
	"context"
	"strings"
	"testing"
	"time"

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
		if want := map[bool]string{true: "local", false: ""}[on]; servers[0].Env[config.EnvComputerUseTarget] != want {
			t.Fatalf("setting %v: target env = %q, want %q", on, servers[0].Env[config.EnvComputerUseTarget], want)
		}
	}
	// A session started with computer use on another computer is bound to it.
	m.SetComputerUseRemote(func(context.Context) (string, string) { return "host-mac", "Studio Mac" })
	if servers := m.aoMCPServers(context.Background(), domain.HarnessClaudeCode, map[string]string{EnvSessionID: "mer-2"}); servers[0].Env[config.EnvComputerUseTarget] != "host-mac" {
		t.Fatalf("remote target env = %v", servers[0].Env)
	}
}

// Terminal sessions on macOS under a desktop-started daemon learn that macOS
// checks Orchestrator, not the agent's own app, so a refused permission sends
// the user to the right place. Chat sessions, other platforms and a daemon
// started from a terminal (where macOS checks that terminal) do not get it.
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
	original, originalOwned := promptGOOS, desktopOwnedDaemon
	t.Cleanup(func() { promptGOOS, desktopOwnedDaemon = original, originalOwned })

	for _, goos := range []string{"darwin", "linux", "windows"} {
		promptGOOS = goos
		for _, desktop := range []bool{true, false} {
			desktopOwnedDaemon = func() bool { return desktop }
			for _, kind := range []domain.SessionKind{domain.KindWorker, domain.KindOrchestrator} {
				for _, chat := range []bool{false, true} {
					sp, err := m.buildSystemPrompt(ctx, kind, "mer", "mer-1", "", chat)
					if err != nil {
						t.Fatalf("buildSystemPrompt(%s, %s, chat=%v): %v", goos, kind, chat, err)
					}
					has := strings.Contains(sp, "## macOS privacy permissions")
					if want := goos == "darwin" && desktop && !chat; has != want {
						t.Fatalf("%s desktop=%v %s chat=%v: section present=%v, want %v", goos, desktop, kind, chat, has, want)
					}
					if has {
						for _, want := range []string{"macOS checks Orchestrator's privacy permissions", "not the permissions of Claude", "Settings > Computer use agents > Native computer use > Grant macOS permissions to Orchestrator", "even if the tool names another app", "restart"} {
							if !strings.Contains(sp, want) {
								t.Fatalf("%s: section missing %q", kind, want)
							}
						}
					}
				}
			}
		}
	}
}

// With computer use running on another of the user's computers, the agent is
// told which one, and that its tools do not act on the machine it runs on.
func TestBuildSystemPromptNamesTheRemoteComputer(t *testing.T) {
	ctx := context.Background()
	st := newFakeStore()
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, DataDir: t.TempDir(),
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	m.SetComputerUseEnabled(func(context.Context) bool { return true })
	label := ""
	m.SetComputerUseRemote(func(context.Context) (string, string) {
		if label == "" {
			return "", ""
		}
		return "host-mac", label
	})

	sp, err := m.buildSystemPrompt(ctx, domain.KindWorker, "mer", "mer-1", "", true)
	if err != nil {
		t.Fatalf("buildSystemPrompt: %v", err)
	}
	if !strings.Contains(sp, "this computer has Cua Driver") || strings.Contains(sp, "another of their computers") {
		t.Fatal("local computer use prompt changed")
	}

	label = "Studio Mac"
	sp, err = m.buildSystemPrompt(ctx, domain.KindWorker, "mer", "mer-2", "", true)
	if err != nil {
		t.Fatalf("buildSystemPrompt: %v", err)
	}
	for _, want := range []string{`another of their computers, "Studio Mac"`, `act on "Studio Mac", not on the machine you run on`, "`get_window_state`", "commands/cua.md", "browser on that computer"} {
		if !strings.Contains(sp, want) {
			t.Fatalf("remote prompt missing %q", want)
		}
	}
	if strings.Contains(sp, "this computer has Cua Driver") {
		t.Fatal("remote prompt still says this computer has Cua Driver")
	}
}

// A starting session's prompt and tool server name the same computer, even
// if the user changes "Run computer use on" between building the two.
func TestStartingSessionSeesOneComputerUseTarget(t *testing.T) {
	ctx := context.Background()
	st := newFakeStore()
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, DataDir: t.TempDir(),
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	m.SetComputerUseEnabled(func(context.Context) bool { return true })
	hostID, label := "host-mac", "Studio Mac"
	m.SetComputerUseRemote(func(context.Context) (string, string) { return hostID, label })

	sp, err := m.buildSystemPrompt(ctx, domain.KindWorker, "mer", "mer-9", "", true)
	if err != nil || !strings.Contains(sp, `"Studio Mac"`) {
		t.Fatalf("prompt = %v, %q", err, sp)
	}
	bound := map[string]string{}
	m.SetComputerUseBinding(func(sessionID, target string) { bound[sessionID] = target })
	hostID, label = "", ""
	servers := m.aoMCPServers(ctx, domain.HarnessClaudeCode, map[string]string{EnvSessionID: "mer-9"})
	if got := servers[0].Env[config.EnvComputerUseTarget]; got != "host-mac" {
		t.Fatalf("tool server target = %q, want the computer the prompt names", got)
	}
	if bound["mer-9"] != "host-mac" {
		t.Fatalf("binding reported = %v, want mer-9 on host-mac", bound)
	}
	// The tool server released the snapshot: the session's next start reads
	// the setting again.
	if hostID, _ := m.computerUseTargetFor(ctx, "mer-9"); hostID != "" {
		t.Fatalf("restart target = %q, want the current setting", hostID)
	}
	// Another session starting now reads the new setting.
	servers = m.aoMCPServers(ctx, domain.HarnessClaudeCode, map[string]string{EnvSessionID: "mer-10"})
	if got := servers[0].Env[config.EnvComputerUseTarget]; got != "local" {
		t.Fatalf("new session target = %q, want local", got)
	}
}

// However long a session takes to start, its tool server gets the target its
// prompt was built with.
func TestSlowStartKeepsItsComputerUseTarget(t *testing.T) {
	ctx := context.Background()
	st := newFakeStore()
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, DataDir: t.TempDir(),
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	m.SetComputerUseEnabled(func(context.Context) bool { return true })
	hostID := "host-mac"
	m.SetComputerUseRemote(func(context.Context) (string, string) { return hostID, "Studio Mac" })
	if got, _ := m.computerUseTargetFor(ctx, "slow-1"); got != "host-mac" {
		t.Fatalf("prompt target = %q", got)
	}
	m.computerUseMu.Lock()
	snapshot := m.computerUseSpawns["slow-1"]
	snapshot.at = snapshot.at.Add(-time.Hour)
	m.computerUseSpawns["slow-1"] = snapshot
	m.computerUseMu.Unlock()
	hostID = ""
	// Another session starting in between must not drop the slow one's snapshot.
	m.computerUseTargetFor(ctx, "other-1")
	servers := m.aoMCPServers(ctx, domain.HarnessClaudeCode, map[string]string{EnvSessionID: "slow-1"})
	if got := servers[0].Env[config.EnvComputerUseTarget]; got != "host-mac" {
		t.Fatalf("tool server target after a slow start = %q, want host-mac", got)
	}
}

// A start that fails after building its prompt and before its tool server
// leaves a snapshot behind; the next start builds its own prompt and gets the
// setting as it is then, not the failed start's target.
func TestFailedStartDoesNotPinTheNextStartsTarget(t *testing.T) {
	ctx := context.Background()
	st := newFakeStore()
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, DataDir: t.TempDir(),
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	enabled := true
	m.SetComputerUseEnabled(func(context.Context) bool { return enabled })
	hostID, label := "host-mac", "Studio Mac"
	m.SetComputerUseRemote(func(context.Context) (string, string) { return hostID, label })

	if _, err := m.buildSystemPrompt(ctx, domain.KindWorker, "mer", "mer-7", "", true); err != nil {
		t.Fatal(err)
	}
	// That start fails before its tool server; the user picks This computer.
	hostID, label = "", ""
	sp, err := m.buildSystemPrompt(ctx, domain.KindWorker, "mer", "mer-7", "", true)
	if err != nil || strings.Contains(sp, `"Studio Mac"`) {
		t.Fatalf("retry prompt = %v, %q, want this computer", err, sp)
	}
	servers := m.aoMCPServers(ctx, domain.HarnessClaudeCode, map[string]string{EnvSessionID: "mer-7"})
	if got := servers[0].Env[config.EnvComputerUseTarget]; got != "local" {
		t.Fatalf("retry tool server target = %q, want local", got)
	}

	// A prompt built without computer use drops a failed start's snapshot too.
	hostID, label = "host-mac", "Studio Mac"
	if _, err := m.buildSystemPrompt(ctx, domain.KindWorker, "mer", "mer-8", "", true); err != nil {
		t.Fatal(err)
	}
	enabled = false
	hostID, label = "host-pc", "Build PC"
	if _, err := m.buildSystemPrompt(ctx, domain.KindWorker, "mer", "mer-8", "", true); err != nil {
		t.Fatal(err)
	}
	m.computerUseMu.Lock()
	_, left := m.computerUseSpawns["mer-8"]
	m.computerUseMu.Unlock()
	if left {
		t.Fatal("a prompt without computer use kept the failed start's snapshot")
	}
}
