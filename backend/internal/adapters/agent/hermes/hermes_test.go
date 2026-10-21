package hermes

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestStandingInstructionsPreserveNativeOverlay(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HERMES_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("agent:\n  system_prompt: Native profile instructions\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HERMES_EPHEMERAL_SYSTEM_PROMPT": "Native invocation instructions"}
	cfg := ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "ao-1", Env: env, SystemPrompt: "AO standing instructions"}
	if err := New().GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if got := env["HERMES_EPHEMERAL_SYSTEM_PROMPT"]; got != "Native invocation instructions\n\nAO standing instructions" {
		t.Fatalf("got %q", got)
	}
	if err := New().GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if strings.Count(env["HERMES_EPHEMERAL_SYSTEM_PROMPT"], "AO standing instructions") != 1 {
		t.Fatal("overlay duplicated")
	}
	env = map[string]string{}
	cfg.Env = env
	if err := New().GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(env["HERMES_EPHEMERAL_SYSTEM_PROMPT"], "Native profile instructions") {
		t.Fatal("lost native profile")
	}
}
func TestRestoreRequiresAOTitledSession(t *testing.T) {
	// A native ID alone, even an exact one, is not an AO-titled conversation.
	for _, id := range []string{"", "latest", "native-id"} {
		_, ok, err := New().GetRestoreCommand(context.Background(), ports.RestoreConfig{DataDir: t.TempDir(), Session: ports.SessionRef{ID: "ao-1", Metadata: map[string]string{ports.MetadataKeyAgentSessionID: id}}})
		if ok || err == nil || !strings.Contains(err.Error(), "title unavailable") {
			t.Fatalf("id %q: ok=%v err=%v", id, ok, err)
		}
	}
}
func TestLaunchRejectsToolRestrictions(t *testing.T) {
	for _, cfg := range []ports.LaunchConfig{{AllowedTools: []string{"read"}}, {DisallowedTools: []string{"bash"}}} {
		if err := validate(cfg); err == nil {
			t.Fatal("unsupported tool restriction accepted")
		}
	}
}

// AO spawns default to auto, so every approval mode must launch. Modes without a
// native tier keep native prompts; only explicit bypass adds --yolo.
func TestPermissionModesMapToNativeApprovals(t *testing.T) {
	for _, tt := range []struct {
		mode ports.PermissionMode
		want []string
	}{
		{"", nil},
		{ports.PermissionModeDefault, nil},
		{ports.PermissionModeAuto, nil},
		{ports.PermissionModeAcceptEdits, nil},
		{ports.PermissionModeBypassPermissions, []string{"--yolo"}},
	} {
		if err := validate(ports.LaunchConfig{Permissions: tt.mode}); err != nil {
			t.Fatalf("%q rejected: %v", tt.mode, err)
		}
		if got := permissionArgs(tt.mode); !slices.Equal(got, tt.want) {
			t.Fatalf("%q args = %v, want %v", tt.mode, got, tt.want)
		}
	}
}
func TestActivityCurrentComposerAndDecision(t *testing.T) {
	for _, tt := range []struct{ screen, want string }{{"☤ ❯ msg=interrupt · /queue · Ctrl+C cancel", "active"}, {"────────────────────────────────────────\n☤ ❯ \n────────────────────────────────────────", "idle"}, {"☤ ❯ \nAllow this command?\n❯ [1] Allow once", "blocked"}, {"unrelated text", ""}} {
		state, ok := New().DetectTerminalActivity(tt.screen)
		if string(state) != tt.want || ok != (tt.want != "") {
			t.Fatalf("%q got %s/%v", tt.screen, state, ok)
		}
	}
}

func TestBorderedBusyComposerStaysActiveAndRefusesMessages(t *testing.T) {
	rule := strings.Repeat("─", 40)
	idle := rule + "\n❯ \n" + rule
	for _, prompt := range []string{
		"☤ ❯ \x1b[38;5;136;3mmsg=interrupt · /queue · Ctrl+C cancel\x1b[m",
		"☤ ❯ \x1b[3mmsg=interrupt · /queue\x1b[m\n\x1b[3mCtrl+C cancel\x1b[m",
	} {
		for _, prefix := range []string{"", idle + "\n"} {
			frame := prefix + rule + "\n" + prompt + "\n" + rule
			state, ok := New().DetectTerminalActivity(frame)
			if !ok || state != domain.ActivityActive {
				t.Fatalf("busy composer classified as %s/%v: %q", state, ok, frame)
			}
			if err := New().ValidateTerminalMessage("next task", frame); err == nil {
				t.Fatalf("running composer admitted ordinary input: %q", frame)
			}
			if !New().TerminalMessageObserved("task", idle, frame) {
				t.Fatalf("running composer did not confirm submission: %q", frame)
			}
		}
	}
	for _, prompt := range []string{"❯ ", "☤ ❯ "} {
		frame := "☤ ❯ msg=interrupt\n" + rule + "\n" + prompt + "\n" + rule
		if state, ok := New().DetectTerminalActivity(frame); !ok || state != domain.ActivityIdle {
			t.Fatalf("prior busy prompt overrode the current idle composer: %s/%v", state, ok)
		}
	}
	if err := New().ValidateTerminalMessage("next task", rule+"\n☤ ❯ keep this draft\n"+rule); err == nil {
		t.Fatal("composer with a draft admitted ordinary input")
	}
}

func TestTerminalMessageObservedRequiresNewCommittedEchoOrBusyFrame(t *testing.T) {
	rule := strings.Repeat("─", 40)
	idle := rule + "\n❯ \n" + rule
	echo := rule + "\n● first task\n" + rule + "\n"
	for _, tt := range []struct {
		name, message, baseline, screen string
		want                            bool
	}{
		{"busy", "first task", idle, "☤ ❯ msg=interrupt", true},
		{"fast turn", "first task", idle, echo + "reply\n" + idle, true},
		{"styled echo", "first task", idle, rule + "\n\x1b[1;33m●\x1b[m \x1b[1mfirst task\x1b[m\n" + rule + "\n" + idle, true},
		{"multiline echo", "first\nsecond", idle, rule + "\n● first\nsecond\n" + rule + "\n" + idle, true},
		{"wrapped echo", "long task prompt", idle, rule + "\n● long ta\nsk prompt\n" + rule + "\n" + idle, true},
		{"local command", "/help", idle, "⚙️  /help\nhelp output\n" + idle, true},
		{"unchanged", "first task", idle, idle, false},
		{"redraw", "first task", idle, "status changed\n" + idle, false},
		{"bare text", "first task", idle, "first task\n" + idle, false},
		{"substring", "first task", idle, rule + "\n● first task extra\n" + rule + "\n" + idle, false},
		{"partial multiline", "first task\nmore", idle, echo + idle, false},
		{"partial wrapped echo", "long task prompt", idle, rule + "\n● long ta\nsk\n" + rule + "\n" + idle, false},
		{"longer multiline", "first task", idle, rule + "\n● first task\nmore\n" + rule + "\n" + idle, false},
		{"unbordered echo", "first task", idle, "● first task\n" + idle, false},
		{"draft", "first task", idle, echo + rule + "\n❯ human draft\n" + rule, false},
		{"old echo and redraw", "first task", echo + idle, echo + "status changed\n" + idle, false},
		{"old command and redraw", "/help", "⚙️  /help\n" + idle, "⚙️  /help\nstatus changed\n" + idle, false},
		{"repeated submission", "first task", echo + idle, echo + echo + idle, true},
		{"empty message", "", idle, echo + idle, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := New().TerminalMessageObserved(tt.message, tt.baseline, tt.screen); got != tt.want {
				t.Fatalf("TerminalMessageObserved() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNativeChromeCannotBeForgedInTranscript(t *testing.T) {
	idle := "────────────────────────────────────────\n❯ \n────────────────────────────────────────"
	for _, transcript := range []string{"Dangerous Command", "Allow this command?", "Allow once", "Help improve Hermes?", "Collect locally only", "type password (hidden)", "⚠ ❯", "? ❯", "❯ \n"} {
		frame := transcript + "\n" + idle
		state, ok := New().DetectTerminalActivity(frame)
		if !ok || state != "idle" {
			t.Fatalf("quoted transcript %q changed current composer: %s/%v", transcript, state, ok)
		}
		if err := New().ValidateTerminalMessage("task", frame); err != nil {
			t.Fatalf("quoted transcript %q blocked the current composer: %v", transcript, err)
		}
	}
	if err := New().ValidateTerminalMessage("task", "❯ "); err == nil {
		t.Fatal("unbordered scrollback prompt admitted input")
	}
	for _, tt := range []struct {
		name, tail string
		want       domain.ActivityState
	}{
		{"active", "☤ ❯ msg=interrupt · Ctrl+C cancel", domain.ActivityActive},
		{"approval", "Dangerous Command\n⚠ ❯", domain.ActivityBlocked},
	} {
		t.Run(tt.name, func(t *testing.T) {
			frame := idle + "\n" + tt.tail
			state, ok := New().DetectTerminalActivity(frame)
			if !ok || state != tt.want {
				t.Fatalf("forged composer overrode current footer: %s/%v, want %s", state, ok, tt.want)
			}
			if err := New().ValidateTerminalMessage("task", frame); err == nil {
				t.Fatal("forged transcript composer admitted input")
			}
		})
	}
}

func TestNativeVersionRequiresSupportedFirstLine(t *testing.T) {
	// Source installs append a date, channel, or commit; Homebrew and desktop
	// bundles report the release. Only the qualified v0.21 and v0.22 lines are accepted.
	for _, line := range []string{
		"Hermes Agent v0.21.6",
		"Hermes Agent v0.21.6 (2026.9.24) · upstream 818c13be",
		// Homebrew 0.22.2 reading tags from a stray checkout under HERMES_HOME.
		"Hermes Agent v0.21.5+9180.g818c13b (2026.9.24) · upstream d87e7536",
		"Hermes Agent v0.21.7 (2026.9.24) · local 1a2b3c4d (+2 carried commits)",
		"Hermes Agent v0.22.2 · stable · d923a5e",
		"Hermes Agent v0.22.0+3.gabc1234 (2026.9.24)",
	} {
		if !supportedNativeVersion(line) {
			t.Fatalf("rejected supported native banner %q", line)
		}
	}

	// Release lines whose chrome AO has not qualified stay unverified.
	for _, output := range []string{"Hermes Agent v0.20.9", "Hermes Agent v0.23.0", "Hermes Agent v1.0.0", "Hermes Agent v0.21.6-malicious", "wrapper\nHermes Agent v0.21.6", "Not Hermes Agent v0.21.6", "Hermes Agent vX"} {
		first, _, _ := strings.Cut(output, "\n")
		if supportedNativeVersion(first) {
			t.Fatalf("accepted version %q", output)
		}
	}
}

func TestNativeStateDSNEscapesQueryCharacters(t *testing.T) {
	dsn := nativeStateDSN("/tmp/hermes?override#fragment")
	if !strings.Contains(dsn, "%3Foverride%23fragment") || !strings.Contains(dsn, "?mode=ro&_pragma=busy_timeout(1000)") {
		t.Fatalf("unsafe native state DSN: %q", dsn)
	}
	if runtime.GOOS == "windows" {
		return // Windows filenames cannot contain '?'.
	}
	fixtureDB := filepath.Join(t.TempDir(), "state.db")
	writer, err := sql.Open("sqlite", fixtureDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Exec(`CREATE TABLE probe(value TEXT); INSERT INTO probe VALUES('correct database')`); err != nil {
		_ = writer.Close()
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "hermes?override#fragment")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(fixtureDB, filepath.Join(home, "state.db")); err != nil {
		t.Fatal(err)
	}
	reader, err := sql.Open("sqlite", nativeStateDSN(home))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	var value string
	if err := reader.QueryRow(`SELECT value FROM probe`).Scan(&value); err != nil || value != "correct database" {
		t.Fatalf("escaped DSN read = %q, err = %v", value, err)
	}
	var timeout int
	if err := reader.QueryRow(`PRAGMA busy_timeout`).Scan(&timeout); err != nil || timeout != 1000 {
		t.Fatalf("busy_timeout = %d, err = %v; want 1000", timeout, err)
	}
	if _, err := reader.Exec(`INSERT INTO probe VALUES('must fail')`); err == nil {
		t.Fatal("escaped DSN lost mode=ro")
	}
}

func TestStandingInstructionsIgnoreDaemonEphemeralPrompt(t *testing.T) {
	t.Setenv("HERMES_EPHEMERAL_SYSTEM_PROMPT", "daemon-only")
	home, data := t.TempDir(), t.TempDir()
	env := map[string]string{"HERMES_HOME": home}
	if err := New().GetAgentHooks(t.Context(), ports.WorkspaceHookConfig{DataDir: data, SessionID: "ao-1", Env: env, SystemPrompt: "session"}); err != nil {
		t.Fatal(err)
	}
	if got := env["HERMES_EPHEMERAL_SYSTEM_PROMPT"]; got != "session" {
		t.Fatalf("inherited daemon prompt: %q", got)
	}
}

func TestStandingInstructionsBudgetIncludesEnvironmentKey(t *testing.T) {
	maxEnvString := 128 * 1024
	if runtime.GOOS == "windows" {
		maxEnvString = 32767
	}
	const promptKey = "HERMES_EPHEMERAL_SYSTEM_PROMPT="
	const originalKey = "AO_HERMES_ORIGINAL_EPHEMERAL="
	original := "native instructions"
	instructionBytesAtLimit := maxEnvString - len(promptKey) - 1 - len(original) - len("\n\n")
	for _, tt := range []struct {
		name             string
		instructionBytes int
		wantError        bool
	}{
		{name: "exact limit", instructionBytes: instructionBytesAtLimit},
		{name: "one byte over", instructionBytes: instructionBytesAtLimit + 1, wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := map[string]string{"HERMES_HOME": t.TempDir(), "HERMES_EPHEMERAL_SYSTEM_PROMPT": original}
			err := New().GetAgentHooks(t.Context(), ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "ao-1", Env: env, SystemPrompt: strings.Repeat("x", tt.instructionBytes)})
			if tt.wantError {
				if err == nil || !strings.Contains(err.Error(), "process environment limit") {
					t.Fatalf("oversized env entry accepted: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := len(promptKey) + len(env["HERMES_EPHEMERAL_SYSTEM_PROMPT"]) + 1; got != maxEnvString {
				t.Fatalf("prompt env entry size = %d, want %d", got, maxEnvString)
			}
			if got := env["AO_HERMES_ORIGINAL_EPHEMERAL"]; got != original {
				t.Fatalf("native prompt copy changed: %q", got)
			}
			if got := len(originalKey) + len(env["AO_HERMES_ORIGINAL_EPHEMERAL"]) + 1; got > maxEnvString {
				t.Fatalf("original env entry size = %d exceeds %d", got, maxEnvString)
			}
		})
	}
}

// fakeNativeBinary installs a shell stand-in at the official POSIX launcher path.
func fakeNativeBinary(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stand-in for the native launcher")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(binDir, "hermes")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\necho 'Hermes Agent v0.21.6'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return binary
}

func TestLeadingDashTaskStaysInPrivateQueryFile(t *testing.T) {
	fakeNativeBinary(t)
	data := t.TempDir()
	cmd, err := New().GetLaunchCommand(context.Background(), ports.LaunchConfig{DataDir: data, SessionID: "ao-1", WorkspacePath: "/workspace", Prompt: "--yolo is task data"})
	if err != nil {
		t.Fatal(err)
	}
	for _, arg := range cmd {
		if arg == "--yolo" {
			t.Fatal("task became option")
		}
	}
	b, err := os.ReadFile(filepath.Join(data, "agents", "hermes", "ao-1", "initial-query.txt"))
	if err != nil || string(b) != "--yolo is task data" {
		t.Fatalf("query=%q err=%v", b, err)
	}
}

func TestNativeLookupDoesNotExecuteEarlierPathCandidate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stand-in for a PATH collision")
	}
	home, pathDir := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	marker := filepath.Join(pathDir, "executed")
	if err := os.WriteFile(filepath.Join(pathDir, "hermes"), []byte("#!/bin/sh\ntouch "+marker+"\necho 'Hermes Agent v0.21.6'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir)
	if path, _ := New().ResolveBinary(t.Context()); path == filepath.Join(pathDir, "hermes") {
		t.Fatal("accepted untrusted PATH candidate")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("PATH candidate was executed: %v", err)
	}
}
func TestPersonalityPreservesNativePrecedence(t *testing.T) {
	overlay := nativeOverlay(map[string]any{"display": map[string]any{"personality": "technical"}, "agent": map[string]any{"system_prompt": "ignored native manual overlay"}})
	if overlay != builtinPersonalities["technical"] {
		t.Fatalf("overlay=%q", overlay)
	}
	overlay = nativeOverlay(map[string]any{"display": map[string]any{"personality": "custom"}, "personalities": map[string]any{"custom": "first"}, "agent": map[string]any{"personalities": map[string]any{"custom": map[string]any{"system_prompt": "native", "tone": "gentle"}}}})
	if overlay != "native\nTone: gentle" {
		t.Fatalf("overlay=%q", overlay)
	}
}

func TestNativeModalChromeBlocks(t *testing.T) {
	for _, screen := range []string{"Dangerous Command\n❯ 1. Allow once\n⚠ ❯", "? ❯"} {
		state, ok := New().DetectTerminalActivity(screen)
		if !ok || state != "blocked" {
			t.Fatalf("modal admitted: %q %s", screen, state)
		}
	}
}

func TestNativeModalLabelsInScrollbackDoNotBlock(t *testing.T) {
	active := "☤ ❯ msg=interrupt · /queue · Ctrl+C cancel"
	for _, label := range []string{"Dangerous Command", "Allow this command?", "Confirm: Allow this command?", "Help improve Hermes?", "Collect locally only", "[1] Allow once", "type secret (hidden)", "type password (hidden)", "type your answer here"} {
		if state, ok := New().DetectTerminalActivity(label); ok {
			t.Fatalf("quoted modal label %q classified as %s", label, state)
		}
		if state, ok := New().DetectTerminalActivity(label + "\n" + active); !ok || state != domain.ActivityActive {
			t.Fatalf("quoted modal label %q overrode active footer: %s/%v", label, state, ok)
		}
	}
	if state, ok := New().DetectTerminalActivity("Dangerous Command\n❯ 1. Allow once\n⚠ ❯\n" + active); !ok || state != domain.ActivityActive {
		t.Fatalf("scrolled modal chrome overrode current active footer: %s/%v", state, ok)
	}
}
func TestStickyProfileFailsClosed(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "active_profile"), []byte("work"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := New().GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "test", Env: map[string]string{"HERMES_HOME": home}, SystemPrompt: "AO"})
	if err == nil {
		t.Fatal("sticky profile silently overridden")
	}
	if !strings.Contains(err.Error(), "clear active_profile") || !strings.Contains(err.Error(), "default native profile") || strings.Contains(err.Error(), "profiles/<name>") {
		t.Fatalf("sticky profile error lacks supported recovery guidance: %v", err)
	}
}

func TestNativeItalicPlaceholderAndDraft(t *testing.T) {
	state, ok := New().DetectTerminalActivity("────────────────────────────────────────\n❯ \x1b[38;5;136;3mPlan a feature\x1b[m\n────────────────────────────────────────")
	if !ok || state != "idle" {
		t.Fatalf("native placeholder %s %v", state, ok)
	}
	if state, ok := New().DetectTerminalActivity("────────────────────────────────────────\n❯ Plan a feature\n────────────────────────────────────────"); !ok || state != domain.ActivityBlocked {
		t.Fatalf("unrecognized composer = %s/%v, want blocked", state, ok)
	}
}

func TestComposerStyleAttributesExcludeColorPayloads(t *testing.T) {
	rule := strings.Repeat("─", 40)
	for _, tt := range []struct {
		name, text string
		want       domain.ActivityState
	}{
		{"palette foreground", "\x1b[38;5;3mdraft\x1b[m", domain.ActivityBlocked},
		{"palette background", "\x1b[48;5;3mdraft\x1b[m", domain.ActivityBlocked},
		{"truecolor foreground", "\x1b[38;2;3;100;100mdraft\x1b[m", domain.ActivityBlocked},
		{"truecolor green operand", "\x1b[38;2;255;3;0mdraft\x1b[m", domain.ActivityBlocked},
		{"truecolor background", "\x1b[48;2;100;3;100mdraft\x1b[m", domain.ActivityBlocked},
		{"underline color", "\x1b[58;5;3mdraft\x1b[m", domain.ActivityBlocked},
		{"colon color", "\x1b[38:2::3:100:100mdraft\x1b[m", domain.ActivityBlocked},
		{"italic then reset", "\x1b[3;0mdraft\x1b[m", domain.ActivityBlocked},
		{"italic then disabled", "\x1b[3;23mdraft\x1b[m", domain.ActivityBlocked},
		{"italic disabled separately", "\x1b[3m\x1b[23mdraft\x1b[m", domain.ActivityBlocked},
		{"italic after color", "\x1b[38;5;23;3mplaceholder\x1b[m", domain.ActivityIdle},
		{"italic before color", "\x1b[3;38;5;23mplaceholder\x1b[m", domain.ActivityIdle},
		{"italic after disable", "\x1b[23;3mplaceholder\x1b[m", domain.ActivityIdle},
		{"italic after colon color", "\x1b[38:2::3:100:23;3mplaceholder\x1b[m", domain.ActivityIdle},
	} {
		t.Run(tt.name, func(t *testing.T) {
			frame := rule + "\n❯ " + tt.text + "\n" + rule
			if state, ok := New().DetectTerminalActivity(frame); !ok || state != tt.want {
				t.Fatalf("composer classified as %s/%v, want %s", state, ok, tt.want)
			}
			if err := New().ValidateTerminalMessage("task", frame); (err == nil) != (tt.want == domain.ActivityIdle) {
				t.Fatalf("message admission disagrees with expected composer state %s: %v", tt.want, err)
			}
		})
	}
}

// writeNativeState creates a minimal native state database with one titled row.
func writeNativeState(t *testing.T, home, id, title string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE sessions(id TEXT PRIMARY KEY, title TEXT, started_at REAL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sessions VALUES(?, ?, 1)`, id, title); err != nil {
		t.Fatal(err)
	}
}

func TestFreshLaunchTitlesNewNativeConversation(t *testing.T) {
	binary := fakeNativeBinary(t)
	data := t.TempDir()
	cfg := ports.LaunchConfig{DataDir: data, SessionID: "agent orchestrator/7", WorkspacePath: "/workspace", Config: ports.AgentConfig{Model: "anthropic/claude-sonnet-4.6", Effort: "high"}}
	first, err := New().GetLaunchCommand(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	title, err := sessionTitle(data, cfg.SessionID)
	if err != nil || !strings.HasPrefix(title, "ao-agent-orchestrator-7-") {
		t.Fatalf("title=%q err=%v", title, err)
	}
	want := []string{binary, "chat", "--cli", "--in", "/workspace", "--continue", title, "--create-if-missing", "--model", "anthropic/claude-sonnet-4.6", "--reasoning", "high"}
	if !slices.Equal(first, want) {
		t.Fatalf("launch = %v, want %v", first, want)
	}
	// A relaunch must never adopt the previous conversation.
	if _, err := New().GetLaunchCommand(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	if again, _ := sessionTitle(data, cfg.SessionID); again == title {
		t.Fatal("fresh launch reused the previous native title")
	}
}

func TestRestoreResumesTitledConversationWithoutCreating(t *testing.T) {
	binary := fakeNativeBinary(t)
	data, home := t.TempDir(), t.TempDir()
	dir := sessionDir(data, "ao-1")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"native-home": home, "native-title": "ao-ao-1-abc"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeNativeState(t, home, "20261020_native", "ao-ao-1-abc")
	cmd, ok, err := New().GetRestoreCommand(t.Context(), ports.RestoreConfig{DataDir: data, Session: ports.SessionRef{ID: "ao-1", WorkspacePath: "/workspace"}, Permissions: ports.PermissionModeBypassPermissions})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if want := []string{binary, "chat", "--cli", "--in", "/workspace", "--continue", "ao-ao-1-abc", "--yolo"}; !slices.Equal(cmd, want) {
		t.Fatalf("restore = %v, want %v", cmd, want)
	}
	id, found, err := New().ResolveNativeSessionID(t.Context(), ports.NativeSessionResolveConfig{DataDir: data, SessionID: "ao-1"})
	if err != nil || !found || id != "20261020_native" {
		t.Fatalf("native id = %q found=%v err=%v", id, found, err)
	}
}

func TestRestoreRefusesMissingOrUnreadableConversation(t *testing.T) {
	for _, tt := range []struct {
		name, state, want string
	}{
		{"renamed or deleted", "", "not found"},
		{"database failure", "not a sqlite database", "native state database unavailable"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data, home := t.TempDir(), t.TempDir()
			dir := sessionDir(data, "ao-1")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			for name, value := range map[string]string{"native-home": home, "native-title": "ao-ao-1-abc"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tt.state == "" {
				writeNativeState(t, home, "other", "someone else's title")
			} else if err := os.WriteFile(filepath.Join(home, "state.db"), []byte(tt.state), 0o600); err != nil {
				t.Fatal(err)
			}
			_, ok, err := New().GetRestoreCommand(t.Context(), ports.RestoreConfig{DataDir: data, Session: ports.SessionRef{ID: "ao-1", WorkspacePath: "/workspace"}})
			if ok || err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ok=%v err=%v, want %q", ok, err, tt.want)
			}
		})
	}
}

func TestProviderFromStatusReadsActiveProvider(t *testing.T) {
	for _, tt := range []struct {
		name, output, provider string
		want                   ports.AgentAuthStatus
	}{
		{"configured provider", "  Model:        (not set)\n  Provider:     OpenAI API\n  Providers:    OpenAI API, Anthropic\n", "OpenAI API", ports.AgentAuthStatusConfigured},
		{"auto with connected providers", "  Provider:     Auto\n  Providers:    Anthropic, GitHub Copilot\n", "Anthropic, GitHub Copilot", ports.AgentAuthStatusConfigured},
		// The Provider row falls back to the requested provider without
		// credentials, and an empty inventory cannot prove one is missing.
		{"explicit label without credentials", "  Provider:     OpenAI API\n  Providers:    none connected\n", "", ports.AgentAuthStatusUnknown},
		{"auto without credentials", "  Provider:     Auto\n  Providers:    none connected\n", "", ports.AgentAuthStatusUnknown},
		{"selected provider not connected", "  Provider:     defunct-provider\n  Providers:    Anthropic\n", "", ports.AgentAuthStatusUnknown},
		{"label only", "  Provider:     Auto\n", "", ports.AgentAuthStatusUnknown},
		// Hermes prints "unknown" when its inventory probe fails.
		{"failed inventory", "  Provider:     Auto\n  Providers:    unknown\n", "", ports.AgentAuthStatusUnknown},
		// Keyless local routes and SDK chains (an AWS profile) are not listed.
		{"keyless local route", "  Provider:     LM Studio\n  Providers:    none connected\n", "", ports.AgentAuthStatusUnknown},
		{"keyless loopback route", "  Provider:     Actual Computer\n  Providers:    none connected\n", "", ports.AgentAuthStatusUnknown},
		{"sdk credential chain", "  Provider:     AWS Bedrock\n  Providers:    none connected\n", "", ports.AgentAuthStatusUnknown},
		{"unrecognized output", "Hermes Agent Status\n", "", ports.AgentAuthStatusUnknown},
	} {
		t.Run(tt.name, func(t *testing.T) {
			provider, status := providerFromStatus(tt.output)
			if provider != tt.provider || status != tt.want {
				t.Fatalf("providerFromStatus = %q, %q; want %q, %q", provider, status, tt.provider, tt.want)
			}
		})
	}
}

func TestCredentialStoreIdentityReadsNativeStores(t *testing.T) {
	for _, key := range hermesCredentialEnv {
		t.Setenv(key, "")
	}
	for _, tt := range []struct {
		name, file, content string
		found               bool
		method              domain.AgentAccountMethod
	}{
		{"no credentials", "", "", false, ""},
		{"empty env key", ".env", "OPENROUTER_API_KEY=\n# ANTHROPIC_API_KEY=sk\n", false, ""},
		{"env api key", ".env", "export OPENROUTER_API_KEY=\"sk-or\"\n", true, domain.AgentAccountMethodAPIKey},
		{"bot token is not a model credential", ".env", "TELEGRAM_BOT_TOKEN=123:abcdefghijklmnopqrstuvw\nTAVILY_API_KEY=t\n", false, ""},
		{"quoted key", ".env", "'ANTHROPIC_API_KEY'=sk-ant\n", true, domain.AgentAccountMethodAPIKey},
		{"oauth store", "auth.json", `{"providers":{"nous":{"access_token":"x"}}}`, true, domain.AgentAccountMethodAccount},
		{"empty oauth store", "auth.json", `{"providers":{}}`, false, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HERMES_HOME", home)
			if tt.file != "" {
				if err := os.WriteFile(filepath.Join(home, tt.file), []byte(tt.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			identity, found := credentialStoreIdentity(t.Context())
			if found != tt.found || identity.Method != tt.method || (found && identity.SourceKind != domain.AgentCredentialSourceFile) {
				t.Fatalf("credentialStoreIdentity = %+v, %v", identity, found)
			}
		})
	}
}

func TestUnattendedReleaseRefreshRemainsGated(t *testing.T) {
	var capability ports.AgentTerminalVersionRefresher = New()
	if capability.SupportsTerminalVersionRefresh() {
		t.Fatal("pinned Hermes native identity does not qualify unattended release changes")
	}
}

func TestHooksForceEnglishChromeAndRefuseDotenvOverrides(t *testing.T) {
	home := t.TempDir()
	env := map[string]string{"HERMES_HOME": home}
	if err := New().GetAgentHooks(t.Context(), ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "ao-1", Env: env, SystemPrompt: "AO"}); err != nil {
		t.Fatal(err)
	}
	if env["HERMES_LANGUAGE"] != "en" {
		t.Fatalf("HERMES_LANGUAGE = %q, want en", env["HERMES_LANGUAGE"])
	}
	hooks := func() error {
		return New().GetAgentHooks(t.Context(), ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "ao-1", Env: map[string]string{"HERMES_HOME": home}, SystemPrompt: "AO"})
	}
	for _, key := range []string{"HERMES_EPHEMERAL_SYSTEM_PROMPT", "HERMES_LANGUAGE"} {
		// Every assignment form python-dotenv accepts must be caught.
		for _, form := range []string{
			"OPENROUTER_API_KEY=x\nexport " + key + "=native\n",
			"'" + key + "'=native\n",
			"export\t" + key + "=native\n",
			"\xef\xbb\xbf" + key + "=native\n",
			key + " = 'multi\nline'\n",
			// Python's \s and str.strip are Unicode: NBSP is whitespace.
			"export\u00a0" + key + "=native\n",
			"\u00a0" + key + "=native\n",
			// Hermes rewrites a UTF-16 (Notepad "Unicode") file as UTF-8 first.
			utf16LE("\ufeff" + key + "=native\r\n"),
		} {
			if err := os.WriteFile(filepath.Join(home, ".env"), []byte(form), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := hooks(); err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("%q in .env accepted: %v", form, err)
			}
		}
	}
	// Malformed encodings are refused: Hermes's replacement-character and
	// latin-1 fallbacks can still apply assignments.
	for _, form := range []string{
		"# legacy comment \xff\n\u00a0HERMES_LANGUAGE=de\n",
		"# legacy comment \xff\n\u00a0HERMES_EPHEMERAL_SYSTEM_PROMPT=native\n",
		"\xffHERMES_LANGUAGE=de\n",
		utf16LE("\ufeffHERMES_LANGUAGE=de\n") + "\x00\xd8",
		"\xff\xfe\x00\x00H\x00\x00\x00",
	} {
		if err := os.WriteFile(filepath.Join(home, ".env"), []byte(form), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := hooks(); err == nil || !strings.Contains(err.Error(), "UTF-8") {
			t.Fatalf("%q in .env accepted: %v", form, err)
		}
	}
	// A key named inside another quoted value is not an assignment.
	if err := os.WriteFile(filepath.Join(home, ".env"), []byte("NOTES='first line\nHERMES_LANGUAGE=just documentation\nlast line'\n# HERMES_LANGUAGE=de\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := hooks(); err != nil {
		t.Fatalf("harmless multiline value rejected: %v", err)
	}
}

// utf16LE encodes s as UTF-16LE bytes in a string.
func utf16LE(s string) string {
	var b []byte
	for _, unit := range utf16.Encode([]rune(s)) {
		b = append(b, byte(unit), byte(unit>>8))
	}
	return string(b)
}

func TestResolveHomeExpandsLikeHermes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX home expansion")
	}
	home := t.TempDir()
	got, err := ResolveHome(map[string]string{"HOME": home, "HERMES_HOME": "~/.hermes-custom"})
	if err != nil || got != filepath.Join(home, ".hermes-custom") {
		t.Fatalf("ResolveHome(~) = %q, %v", got, err)
	}
	got, err = ResolveHome(map[string]string{"HOME": home, "HERMES_HOME": "$HOME/h-${SUFFIX}", "SUFFIX": "x"})
	if err != nil || got != filepath.Join(home, "h-x") {
		t.Fatalf("ResolveHome($VAR) = %q, %v", got, err)
	}
	// Variables expand before ~, as os.path.expanduser(os.path.expandvars(...)).
	got, err = ResolveHome(map[string]string{"HOME": home, "HERMES_PARENT": "~", "HERMES_HOME": "$HERMES_PARENT/custom"})
	if err != nil || got != filepath.Join(home, "custom") {
		t.Fatalf("ResolveHome(var then ~) = %q, %v", got, err)
	}
	// An undefined variable stays literal, so AO never picks another directory.
	got, err = ResolveHome(map[string]string{"HOME": home, "HERMES_HOME": "/srv/$AO_REVIEW_UNDEFINED/custom"})
	if err != nil || got != "/srv/$AO_REVIEW_UNDEFINED/custom" {
		t.Fatalf("ResolveHome(undefined) = %q, %v", got, err)
	}
	// An explicitly empty child variable is defined and expands to empty.
	got, err = ResolveHome(map[string]string{"HOME": home, "HERMES_HOME": "${AO_REVIEW_EMPTY}/custom", "AO_REVIEW_EMPTY": ""})
	if err != nil || got != "/custom" {
		t.Fatalf("ResolveHome(empty child var) = %q, %v", got, err)
	}
	// Hermes resolves a relative home against the session workspace, not the
	// daemon's directory, so it is refused rather than inspected elsewhere.
	if got, err = ResolveHome(map[string]string{"HOME": home, "HERMES_HOME": ".ao-review-relative-hermes"}); err == nil {
		t.Fatalf("ResolveHome(relative) = %q, want error", got)
	}
	cfg := ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "ao-1", WorkspacePath: t.TempDir(), Env: map[string]string{"HERMES_HOME": ".ao-review-relative-hermes"}, SystemPrompt: "AO"}
	if err := New().GetAgentHooks(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative HERMES_HOME accepted: %v", err)
	}
}

func TestExpandWindowsVarsMatchesNtpath(t *testing.T) {
	lookup := func(name string) (string, bool) {
		value, ok := map[string]string{"APPDATA": `C:\A`, "X": "x", "EMPTY": ""}[name]
		return value, ok
	}
	for in, want := range map[string]string{
		`%APPDATA%\hermes`: `C:\A\hermes`,
		`$X\${X}`:          `x\x`,
		`$X-`:              `$X-`, // ntpath names may contain '-', so this is undefined X-
		`%MISSING%\h`:      `%MISSING%\h`,
		`'%APPDATA%'\h`:    `'%APPDATA%'\h`,
		`100%%`:            "100%",
		`%EMPTY%\h`:        `\h`,
	} {
		if got := expandWindowsVars(in, lookup); got != want {
			t.Fatalf("expandWindowsVars(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCompactBusyChromeWitnessesSubmission(t *testing.T) {
	rule := strings.Repeat("─", 60)
	idle := rule + "\n❯ "
	busy := rule + "\n\x1b[3m☤ msg=interrupt · /queue · /bg · /steer · Ctrl+C cancel\x1b[m"
	if !New().TerminalMessageObserved("task", idle, busy) {
		t.Fatal("compact busy frame did not witness the submission")
	}
}

func TestConfirmedIdentityFollowsInstallRevision(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HERMES_HOME", home)
	gitDir := filepath.Join(home, "hermes-agent", ".git", "refs", "heads")
	if err := os.MkdirAll(gitDir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, "hermes-agent", ".git", "HEAD"), "ref: refs/heads/main\n")
	write(filepath.Join(gitDir, "main"), "1111111111111111111111111111111111111111\n")
	launcher := filepath.Join(home, "hermes")
	write(launcher, "#!/bin/sh\n")
	rememberIdentity(launcher)
	if !identityConfirmed(launcher) {
		t.Fatal("fresh confirmation not reused")
	}
	// A source update moves the checkout without touching the launcher.
	write(filepath.Join(gitDir, "main"), "2222222222222222222222222222222222222222\n")
	if identityConfirmed(launcher) {
		t.Fatal("confirmation survived an installation revision change")
	}
	rememberIdentity(launcher)
	forgetIdentity(launcher)
	if identityConfirmed(launcher) {
		t.Fatal("a rejected probe left the launcher confirmed")
	}
}

func TestConfirmedIdentityFollowsLinkedWorktreeRevision(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	home, repo := t.TempDir(), t.TempDir()
	t.Setenv("HERMES_HOME", home)
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git(repo, "init", "-q", "-b", "main")
	git(repo, "commit", "-q", "--allow-empty", "-m", "one")
	worktree := filepath.Join(home, "hermes-agent")
	git(repo, "worktree", "add", "-q", "-b", "runtime", worktree, "main")
	launcher := filepath.Join(home, "hermes")
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	rememberIdentity(launcher)
	if !identityConfirmed(launcher) {
		t.Fatal("fresh confirmation not reused")
	}
	// Branch refs of a linked worktree live in the shared git directory.
	git(worktree, "commit", "-q", "--allow-empty", "-m", "two")
	if identityConfirmed(launcher) {
		t.Fatal("confirmation survived a commit in a linked worktree")
	}
}

func TestCompactComposerBelow64ColumnsIsIdle(t *testing.T) {
	rule := strings.Repeat("─", 40)
	for _, tt := range []struct {
		name, screen string
		want         domain.ActivityState
	}{
		{"compact idle", "transcript\n" + rule + "\n☤ ❯ ", domain.ActivityIdle},
		{"compact draft", "transcript\n" + rule + "\n☤ ❯ typed draft", domain.ActivityBlocked},
		{"compact busy", "transcript\n" + rule + "\n☤ ❯ msg=interrupt · Ctrl+C cancel", domain.ActivityActive},
		// Native compact chrome draws only the state icon, without ❯.
		{"compact busy icon", "transcript\n" + rule + "\n\x1b[3m☤ msg=interrupt · /queue · /bg · /steer · Ctrl+C cancel\x1b[m", domain.ActivityActive},
		{"compact approval icon", "transcript\n" + rule + "\n⚠ ", domain.ActivityBlocked},
		{"compact question icon", "transcript\n" + rule + "\n? ", domain.ActivityBlocked},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got, ok := New().DetectTerminalActivity(tt.screen); !ok || got != tt.want {
				t.Fatalf("DetectTerminalActivity = %s/%v, want %s", got, ok, tt.want)
			}
		})
	}
}
