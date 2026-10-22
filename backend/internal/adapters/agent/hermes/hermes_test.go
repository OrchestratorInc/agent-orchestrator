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
	overlay, _, _ := nativeOverlay(map[string]any{"display": map[string]any{"personality": "technical"}, "agent": map[string]any{"system_prompt": "ignored native manual overlay"}})
	if overlay != builtinPersonalities["technical"] {
		t.Fatalf("overlay=%q", overlay)
	}
	overlay, _, _ = nativeOverlay(map[string]any{"display": map[string]any{"personality": "custom"}, "personalities": map[string]any{"custom": "first"}, "agent": map[string]any{"personalities": map[string]any{"custom": map[string]any{"system_prompt": "native", "tone": "gentle"}}}})
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
		// Hermes keeps an empty pool list after removing the last credential.
		{"pool credential", "auth.json", `{"version":1,"providers":{},"credential_pool":{"openai-api":[{"id":"a","access_token":"x"}]}}`, true, domain.AgentAccountMethodAccount},
		{"removed final pool credential", "auth.json", `{"version":1,"providers":{},"credential_pool":{"openai-api":[]}}`, false, ""},
		// The last assignment is the one Hermes keeps.
		{"superseded env key", ".env", "OPENROUTER_API_KEY=review-dummy-key\nOPENROUTER_API_KEY=\n", false, ""},
		{"blank env key", ".env", "OPENROUTER_API_KEY=\"   \"\n", false, ""},
		// Hermes keeps an interior U+FEFF in the name, so this is another variable.
		{"bom-prefixed env key", ".env", "NOTES=ordinary\n\ufeffOPENROUTER_API_KEY=sk-or-review-dummy\n", false, ""},
		// A borrowed env-key reference stays after its source variable is gone.
		{"secret-free pool reference", "auth.json", `{"version":1,"providers":{},"credential_pool":{"openrouter":[{"id":"a","source":"env:OPENROUTER_API_KEY","label":"OPENROUTER_API_KEY","auth_type":"api_key","access_token":""}]}}`, false, ""},
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
		forms := []string{
			"OPENROUTER_API_KEY=x\nexport " + key + "=native\n",
			"'" + key + "'=native\n",
			"export\t" + key + "=native\n",
			"\xef\xbb\xbf" + key + "=native\n",
			key + " = 'multi\nline'\n",
			// Python's \s and str.strip are Unicode: NBSP is whitespace.
			"export\u00a0" + key + "=native\n",
			"\u00a0" + key + "=native\n",
			// Hermes rewrites a UTF-16 (Notepad "Unicode") file as UTF-8 first,
			// and a U+FEFF surviving either decode is dropped by utf-8-sig.
			utf16LE("\ufeff" + key + "=native\r\n"),
			utf16LE("\ufeff\ufeff" + key + "=native\n"),
			"\xef\xbb\xbf\ufeff" + key + "=native\n",
		}
		if runtime.GOOS == "windows" {
			// Windows os.environ upper-cases names.
			forms = append(forms, strings.ToLower(key)+"=native\n")
			// Python's full upper-casing expands the U+FB06 "st" ligature.
			if strings.Contains(key, "ST") {
				forms = append(forms, strings.Replace(strings.ToLower(key), "st", "\ufb06", 1)+"=native\n")
			}
		}
		for _, form := range forms {
			if err := os.WriteFile(filepath.Join(home, ".env"), []byte(form), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := hooks(); err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("%q in .env accepted: %v", form, err)
			}
		}
	}
	// A home or managed-scope redirect would load another, unchecked .env.
	for _, key := range []string{"HERMES_HOME", "HERMES_MANAGED_DIR"} {
		if err := os.WriteFile(filepath.Join(home, ".env"), []byte(key+"="+t.TempDir()+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := hooks(); err == nil || !strings.Contains(err.Error(), key) {
			t.Fatalf("%s in .env accepted: %v", key, err)
		}
	}
	if err := os.Remove(filepath.Join(home, ".env")); err != nil {
		t.Fatal(err)
	}
	// A source install's project .env loads with override when the home
	// .env is absent.
	if err := os.MkdirAll(filepath.Join(home, "hermes-agent"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "hermes-agent", ".env"), []byte("HERMES_LANGUAGE=de\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := hooks(); err == nil || !strings.Contains(err.Error(), "HERMES_LANGUAGE") {
		t.Fatalf("project .env override accepted: %v", err)
	}
	if err := os.Remove(filepath.Join(home, "hermes-agent", ".env")); err != nil {
		t.Fatal(err)
	}
	// HERMES_MANAGED_DIR resolves against the session workspace.
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, ".ao-review-managed"), 0o700); err != nil {
		t.Fatal(err)
	}
	managed := ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "ao-1", WorkspacePath: workspace, Env: map[string]string{"HERMES_HOME": home, "HERMES_MANAGED_DIR": ".ao-review-managed"}, SystemPrompt: "AO"}
	if err := New().GetAgentHooks(t.Context(), managed); err == nil || !strings.Contains(err.Error(), "managed") {
		t.Fatalf("workspace-relative managed scope accepted: %v", err)
	}
	// Python's str.strip also removes U+001C..U+001F.
	for _, prefix := range []string{"\x1c", "\x1f", "\u00a0"} {
		managed.Env = map[string]string{"HERMES_HOME": home, "HERMES_MANAGED_DIR": prefix + filepath.Join(workspace, ".ao-review-managed")}
		if err := New().GetAgentHooks(t.Context(), managed); err == nil || !strings.Contains(err.Error(), "managed") {
			t.Fatalf("managed scope with %q prefix accepted: %v", prefix, err)
		}
	}
	// The filesystem follows a symlink before "..", unlike filepath.Join.
	if runtime.GOOS != "windows" {
		external := t.TempDir()
		for _, dir := range []string{"target", "managed"} {
			if err := os.Mkdir(filepath.Join(external, dir), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Symlink(filepath.Join(external, "target"), filepath.Join(workspace, "link")); err != nil {
			t.Fatal(err)
		}
		managed.Env = map[string]string{"HERMES_HOME": home, "HERMES_MANAGED_DIR": "link/../managed"}
		if err := New().GetAgentHooks(t.Context(), managed); err == nil || !strings.Contains(err.Error(), "managed") {
			t.Fatalf("symlinked managed scope accepted: %v", err)
		}
	}
	// .op.env bootstraps the environment before the managed scope is read.
	if err := os.WriteFile(filepath.Join(home, ".op.env"), []byte("HERMES_MANAGED_DIR="+t.TempDir()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := hooks(); err == nil || !strings.Contains(err.Error(), "HERMES_MANAGED_DIR") {
		t.Fatalf(".op.env managed scope accepted: %v", err)
	}
	if err := os.Remove(filepath.Join(home, ".op.env")); err != nil {
		t.Fatal(err)
	}
	// The source checkout stays under the default home when HERMES_HOME moves.
	userHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(userHome, ".hermes", "hermes-agent"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userHome, ".hermes", "hermes-agent", ".env"), []byte("HERMES_LANGUAGE=de\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	separate := ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "ao-1", Env: map[string]string{"HOME": userHome, "USERPROFILE": userHome, "LOCALAPPDATA": filepath.Join(userHome, ".hermes"), "HERMES_HOME": t.TempDir()}, SystemPrompt: "AO"}
	if runtime.GOOS == "windows" {
		separate.Env["LOCALAPPDATA"] = userHome
		if err := os.Rename(filepath.Join(userHome, ".hermes"), filepath.Join(userHome, "hermes")); err != nil {
			t.Fatal(err)
		}
	}
	if err := New().GetAgentHooks(t.Context(), separate); err == nil || !strings.Contains(err.Error(), "HERMES_LANGUAGE") {
		t.Fatalf("installed checkout's project .env accepted: %v", err)
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
	// The default derived from a relative HOME is just as relative.
	if got, err = ResolveHome(map[string]string{"HERMES_HOME": "", "HOME": ".ao-review-relative-user"}); err == nil {
		t.Fatalf("ResolveHome(relative HOME) = %q, want error", got)
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

// gitCheckout initializes a real repository with one commit and returns a
// helper that runs git in it.
func gitCheckout(t *testing.T, dir string) func(args ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "one")
	return git
}

func TestConfirmedIdentityFollowsInstallRevision(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HERMES_HOME", home)
	git := gitCheckout(t, filepath.Join(home, "hermes-agent"))
	launcher := filepath.Join(home, "hermes-agent", ".hermes", "bin", "hermes")
	if err := os.MkdirAll(filepath.Dir(launcher), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	rememberIdentity(launcher)
	if !identityConfirmed(t.Context(), launcher) {
		t.Fatal("fresh confirmation not reused")
	}
	// A source update moves the checkout without touching the launcher.
	git("commit", "-q", "--allow-empty", "-m", "two")
	if identityConfirmed(t.Context(), launcher) {
		t.Fatal("confirmation survived an installation revision change")
	}
	rememberIdentity(launcher)
	forgetIdentity(launcher)
	if identityConfirmed(t.Context(), launcher) {
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
	launcher := filepath.Join(worktree, ".hermes", "bin", "hermes")
	if err := os.MkdirAll(filepath.Dir(launcher), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	rememberIdentity(launcher)
	if !identityConfirmed(t.Context(), launcher) {
		t.Fatal("fresh confirmation not reused")
	}
	// Branch refs of a linked worktree live in the shared git directory.
	git(worktree, "commit", "-q", "--allow-empty", "-m", "two")
	if identityConfirmed(t.Context(), launcher) {
		t.Fatal("confirmation survived a commit in a linked worktree")
	}
}

func TestConfirmedIdentityFollowsWrapperSourceRevision(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell wrapper")
	}
	t.Setenv("HERMES_HOME", t.TempDir())
	// A checkout installed outside HERMES_HOME (install.sh --dir).
	src := t.TempDir()
	git := gitCheckout(t, src)
	launcher := filepath.Join(src, ".hermes", "bin", "hermes")
	if err := os.MkdirAll(filepath.Dir(launcher), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(t.TempDir(), "hermes")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec "+launcher+" \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got, want := launcherSourceRoot(wrapper), filepath.Clean(src); got != want {
		resolved, _ := filepath.EvalSymlinks(want)
		if got != resolved {
			t.Fatalf("launcherSourceRoot = %q, want %q", got, want)
		}
	}
	rememberIdentity(wrapper)
	if !identityConfirmed(t.Context(), wrapper) {
		t.Fatal("fresh confirmation not reused")
	}
	git("commit", "-q", "--allow-empty", "-m", "two")
	if identityConfirmed(t.Context(), wrapper) {
		t.Fatal("confirmation survived a commit in the wrapper's source checkout")
	}
}

func TestSourceCleanlinessMatchesNativeTopology(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX paths and symlinks")
	}
	for _, name := range []string{"source", "source ends in space "} {
		root := filepath.Join(t.TempDir(), name)
		git := gitCheckout(t, root)
		if err := os.WriteFile(filepath.Join(root, "en.yaml"), []byte("busy: Working\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		git("add", "en.yaml")
		git("commit", "-q", "-m", "release")
		if !sourceUnmodified(t.Context(), root) {
			t.Fatalf("%q: clean checkout reported modified", name)
		}
		// git prints the root with its trailing space; it must still match.
		if err := os.WriteFile(filepath.Join(root, "en.yaml"), []byte("busy: REVIEW DIRTY ROOT BUSY\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if sourceUnmodified(t.Context(), root) {
			t.Fatalf("%q: tracked edit accepted", name)
		}
		git("checkout", "-q", "--", "en.yaml")
		// Native discovery follows an untracked plugin symlink git never enters.
		external := t.TempDir()
		if err := os.MkdirAll(filepath.Join(external, "locales"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(external, "locales", "en.yaml"), []byte("cli: {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, "plugins"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(external, filepath.Join(root, "plugins", "linked-pack")); err != nil {
			t.Fatal(err)
		}
		if sourceUnmodified(t.Context(), root) {
			t.Fatalf("%q: untracked plugin symlink accepted", name)
		}
	}
	// A git checkout git cannot read is not verified clean.
	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, ".git"), []byte("gitdir: /nonexistent\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if sourceUnmodified(t.Context(), broken) {
		t.Fatal("unverifiable checkout accepted")
	}
}

func TestFirstShellWordMatchesShlexJoin(t *testing.T) {
	for in, want := range map[string]string{
		`/a/b/.hermes/bin/hermes "$@"`:                    "/a/b/.hermes/bin/hermes",
		`'/a/with space/.hermes/bin/hermes' "$@"`:         "/a/with space/.hermes/bin/hermes",
		`'/a/Owner'"'"'s Hermes/.hermes/bin/hermes' "$@"`: "/a/Owner's Hermes/.hermes/bin/hermes",
		`/a/x\ y/hermes "$@"`:                             "/a/x y/hermes",
		`"/a/q\"d/hermes" "$@"`:                           `/a/q"d/hermes`,
		`"$HOME/hermes" "$@"`:                             "",
		`/a/$(id)/hermes`:                                 "",
		`'/a/unterminated`:                                "",
	} {
		if got := firstShellWord(in); got != want {
			t.Errorf("firstShellWord(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestApostropheWrapperResolvesSourceRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell wrapper")
	}
	src := filepath.Join(t.TempDir(), "Owner's Hermes")
	launcher := filepath.Join(src, ".hermes", "bin", "hermes")
	if err := os.MkdirAll(filepath.Dir(launcher), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	quoted := "'" + strings.ReplaceAll(launcher, "'", `'"'"'`) + "'"
	wrapper := filepath.Join(t.TempDir(), "hermes")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec "+quoted+" \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(src)
	if got := launcherSourceRoot(wrapper); got != want {
		t.Fatalf("launcherSourceRoot = %q, want %q", got, want)
	}
	// A wrapper whose checkout AO cannot name is never served from cache.
	opaque := filepath.Join(t.TempDir(), "hermes")
	if err := os.WriteFile(opaque, []byte("#!/bin/sh\nexec \"$HERMES_ROOT/hermes\" \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	rememberIdentity(opaque)
	if identityConfirmed(t.Context(), opaque) {
		t.Fatal("unresolvable wrapper reused a cached confirmation")
	}
}

func TestInheritedCredentialClearedByProjectDotenv(t *testing.T) {
	for _, key := range hermesCredentialEnv {
		t.Setenv(key, "")
	}
	home := t.TempDir()
	t.Setenv("HERMES_HOME", home)
	t.Setenv("HERMES_MANAGED_DIR", "")
	t.Setenv("OPENROUTER_API_KEY", "sk-or-review-dummy-key")
	if err := os.MkdirAll(filepath.Join(home, "hermes-agent"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "hermes-agent", ".env"), []byte("OPENROUTER_API_KEY=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Without a home .env, Hermes loads the project .env with override.
	if identity, found := credentialStoreIdentity(t.Context()); found {
		t.Fatalf("key cleared by project .env reported: %+v", identity)
	}
	// With one, the project .env loads without override and cannot clear it.
	if err := os.WriteFile(filepath.Join(home, ".env"), []byte("NOTES=ordinary\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, found := credentialStoreIdentity(t.Context()); !found {
		t.Fatal("inherited key not found when the home .env leaves it alone")
	}
}

func TestInheritedCredentialClearedByHomeDotenv(t *testing.T) {
	for _, key := range hermesCredentialEnv {
		t.Setenv(key, "")
	}
	home := t.TempDir()
	t.Setenv("HERMES_HOME", home)
	t.Setenv("OPENROUTER_API_KEY", "sk-or-review-dummy-key")
	if _, found := credentialStoreIdentity(t.Context()); !found {
		t.Fatal("inherited key without a home .env not found")
	}
	// Hermes loads its home .env with override.
	if err := os.WriteFile(filepath.Join(home, ".env"), []byte("OPENROUTER_API_KEY=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if identity, found := credentialStoreIdentity(t.Context()); found {
		t.Fatalf("key cleared by home .env reported: %+v", identity)
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

func TestHooksIgnoreEnvExamplesInConfigComments(t *testing.T) {
	home := t.TempDir()
	hooks := func(config string) error {
		if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
		return New().GetAgentHooks(t.Context(), ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "ao-1", Env: map[string]string{"HERMES_HOME": home}, SystemPrompt: "AO"})
	}
	// The installer's generated config documents ${VAR} in comments.
	if err := hooks("# api_key: ${OPENROUTER_API_KEY}\nmodel:\n  default: gpt\n  base_url: ${CUSTOM_BASE_URL}\n"); err != nil {
		t.Fatalf("comment or non-instruction ${VAR} refused: %v", err)
	}
	for _, config := range []string{
		"agent:\n  system_prompt: \"Use ${PROJECT}\"\n",
		"display:\n  personality: mine\npersonalities:\n  mine: \"I am ${NAME}\"\n",
		"display:\n  personality: mine\nagent:\n  personalities:\n    mine:\n      system_prompt: ${PROMPT}\n",
		"display:\n  personality: mine\npersonalities:\n  mine:\n    system_prompt: ok\n    tone: ${TONE}\n",
		"display:\n  personality: mine\npersonalities:\n  mine:\n    system_prompt: ok\n    style: ${STYLE}\n",
		"display:\n  personality: mine\npersonalities:\n  mine:\n    - first\n    - ${LINE}\n",
		"display:\n  personality: ${CHOICE}\n",
	} {
		if err := hooks(config); err == nil || !strings.Contains(err.Error(), "environment-expanded") {
			t.Fatalf("instruction ${VAR} accepted for %q: %v", config, err)
		}
	}
	// Hermes renders only system_prompt, tone and style of the selected
	// personality; a description is list-preview text.
	for _, config := range []string{
		"display:\n  personality: mine\npersonalities:\n  mine:\n    description: \"${LABEL}\"\n    system_prompt: Native overlay\n",
		"display:\n  personality: mine\nagent:\n  personalities:\n    mine:\n      description: \"${LABEL}\"\n      system_prompt: Native overlay\n",
		"display:\n  personality: helpful\npersonalities:\n  other: \"${UNSELECTED}\"\n",
		"agent:\n  personalities:\n    mine:\n      system_prompt: ${UNSELECTED}\n",
	} {
		if err := hooks(config); err != nil {
			t.Fatalf("unrendered ${VAR} refused for %q: %v", config, err)
		}
	}
}

func TestTaglessSourceBannerNeedsQualifiedTagAtHead(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "release")
	launcher := filepath.Join(root, ".hermes", "bin", "hermes")
	if err := os.MkdirAll(filepath.Dir(launcher), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	head := git("rev-parse", "HEAD")
	banner := "Hermes Agent vgit." + head[:7] + " (2026.9.24) · stable · local " + head[:12]
	if qualifiedSourceCheckout(t.Context(), launcher, banner) {
		t.Fatal("untagged checkout accepted")
	}
	// An annotated tag is a loose ref and object, then packed with a peel line.
	git("tag", "-a", "v0.22.2", "-m", "v0.22.2")
	if !qualifiedSourceCheckout(t.Context(), launcher, banner) {
		t.Fatal("loose annotated qualified tag at HEAD rejected")
	}
	git("pack-refs", "--all")
	if !qualifiedSourceCheckout(t.Context(), launcher, banner) {
		t.Fatal("packed annotated qualified tag at HEAD rejected")
	}
	for _, line := range []string{
		"Hermes Agent vgit." + head[:7] + ".dirty (2026.9.24)",
		"Hermes Agent vgit.0000000 (2026.9.24)",
	} {
		if qualifiedSourceCheckout(t.Context(), launcher, line) {
			t.Fatalf("%q accepted", line)
		}
	}
	// A tracked modification is not the release, whatever the banner says.
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "tracked.txt")
	git("commit", "-q", "-m", "tracked")
	git("tag", "-d", "v0.22.2")
	git("tag", "-a", "v0.22.2", "-m", "v0.22.2")
	head = git("rev-parse", "HEAD")
	banner = "Hermes Agent vgit." + head[:7] + " (2026.9.24) · stable · local " + head[:12]
	if !qualifiedSourceCheckout(t.Context(), launcher, banner) {
		t.Fatal("clean tagged checkout rejected")
	}
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("REVIEW LOCAL BUSY\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if qualifiedSourceCheckout(t.Context(), launcher, banner) {
		t.Fatal("tracked modification accepted")
	}
	git("checkout", "-q", "--", "tracked.txt")
	// A loose ref can name an annotated tag object stored in a pack.
	tagObject := git("rev-parse", "refs/tags/v0.22.2")
	git("repack", "-q", "-a", "-d")
	git("update-ref", "-d", "refs/tags/v0.22.2")
	git("update-ref", "refs/tags/v0.22.2", tagObject)
	if !qualifiedSourceCheckout(t.Context(), launcher, banner) {
		t.Fatal("loose ref to a packed annotated tag rejected")
	}
	// A loose ref shadows a stale packed entry of the same name.
	git("pack-refs", "--all")
	release := head
	git("commit", "-q", "--allow-empty", "-m", "retag")
	git("tag", "-f", "v0.22.2", "HEAD")
	git("checkout", "-q", "--detach", release)
	if qualifiedSourceCheckout(t.Context(), launcher, banner) {
		t.Fatal("stale packed tag shadowed by a loose ref accepted")
	}
	git("checkout", "-q", "main")
	git("tag", "-f", "v0.22.2", release)
	git("checkout", "-q", "--detach", release)
	// A commit past the tag, or an unqualified line, is not that release.
	git("tag", "v0.23.0")
	git("tag", "-d", "v0.22.2")
	if qualifiedSourceCheckout(t.Context(), launcher, banner) {
		t.Fatal("unqualified release tag accepted")
	}
	git("tag", "v0.22.2")
	git("commit", "-q", "--allow-empty", "-m", "after")
	head = git("rev-parse", "HEAD")
	if qualifiedSourceCheckout(t.Context(), launcher, "Hermes Agent vgit."+head[:7]+" (2026.9.24)") {
		t.Fatal("commit past the release tag accepted")
	}
}

func TestDotenvValuesFollowPythonDotenv(t *testing.T) {
	for _, key := range hermesCredentialEnv {
		t.Setenv(key, "")
	}
	t.Setenv("HERMES_MANAGED_DIR", "")
	for content, found := range map[string]bool{
		// ${VAR} and ${VAR:-default} resolve before the value is used.
		"OPENROUTER_API_KEY=${AO_REVIEW_MISSING}\n":     false,
		"OPENROUTER_API_KEY=${AO_REVIEW_MISSING:-sk}\n": true,
		// Double-quoted escapes decode, so this is a tab: blank.
		"OPENROUTER_API_KEY=\"\\t\"\n": false,
		// A bare name sets nothing.
		"OPENROUTER_API_KEY\n": false,
	} {
		home := t.TempDir()
		t.Setenv("HERMES_HOME", home)
		if err := os.WriteFile(filepath.Join(home, ".env"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, got := credentialStoreIdentity(t.Context()); got != found {
			t.Errorf("%q: found = %v, want %v", content, got, found)
		}
	}
	// A bare protected name without "=" is not an assignment.
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".env"), []byte("HERMES_LANGUAGE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := New().GetAgentHooks(t.Context(), ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "ao-1", Env: map[string]string{"HERMES_HOME": home}, SystemPrompt: "AO"}); err != nil {
		t.Fatalf("bare name refused: %v", err)
	}
}

func TestHooksRefuseLayersThatReplaceAOValues(t *testing.T) {
	t.Setenv("HERMES_BUNDLED_LOCALES", "")
	hooks := func(home, workspace string, env map[string]string) error {
		if env == nil {
			env = map[string]string{}
		}
		env["HERMES_HOME"] = home
		return New().GetAgentHooks(t.Context(), ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "ao-1", WorkspacePath: workspace, Env: env, SystemPrompt: "AO"})
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// External secret sources may override; only override_existing: false is safe.
	for config, refused := range map[string]bool{
		"secrets:\n  bitwarden:\n    enabled: true\n":                               true,
		"secrets:\n  onepassword:\n    enabled: yes\n":                              true,
		"secrets:\n  bitwarden:\n    enabled: true\n    override_existing: on\n":    true,
		"secrets:\n  bitwarden:\n    enabled: true\n    override_existing: no\n":    false,
		"secrets:\n  bitwarden:\n    enabled: true\n    override_existing: false\n": false,
		"secrets:\n  bitwarden:\n    enabled: false\n":                              false,
	} {
		home := t.TempDir()
		write(filepath.Join(home, "config.yaml"), config)
		env := map[string]string{}
		if err := hooks(home, "", env); (err != nil) != refused {
			t.Errorf("%q: err = %v, want refused=%v", config, err, refused)
		}
		if !refused && (env["HERMES_MANAGED_DIR"] != "" || env["HERMES_BUNDLED_LOCALES"] != "") {
			t.Errorf("%q: redirects not pinned empty: %v", config, env)
		}
		if _, pinned := env["HERMES_MANAGED_DIR"]; !refused && !pinned {
			t.Errorf("%q: HERMES_MANAGED_DIR not pinned", config)
		}
	}
	// English locale layers reword the chrome AO reads.
	for _, rel := range []string{"locales/en.yaml", "locales/en.tui.yaml", "plugins/pack/locales/en.yaml", "plugins/category/pack/locales/en.core.yml"} {
		home := t.TempDir()
		write(filepath.Join(home, rel), "cli: {}\n")
		if err := hooks(home, "", nil); err == nil || !strings.Contains(err.Error(), "English") {
			t.Errorf("%s accepted: %v", rel, err)
		}
	}
	home, workspace := t.TempDir(), t.TempDir()
	write(filepath.Join(home, "locales", "de.yaml"), "cli: {}\n")
	if err := hooks(home, workspace, nil); err != nil {
		t.Fatalf("non-English overlay refused: %v", err)
	}
	write(filepath.Join(workspace, ".hermes", "plugins", "pack", "locales", "en.yaml"), "cli: {}\n")
	if err := hooks(home, workspace, nil); err == nil {
		t.Fatal("project plugin English pack accepted")
	}
	bundled := t.TempDir()
	if err := hooks(t.TempDir(), "", map[string]string{"HERMES_BUNDLED_LOCALES": bundled}); err == nil {
		t.Fatal("HERMES_BUNDLED_LOCALES accepted")
	}
	home = t.TempDir()
	write(filepath.Join(home, ".env"), "HERMES_BUNDLED_LOCALES="+bundled+"\n")
	if err := hooks(home, "", nil); err == nil {
		t.Fatal("HERMES_BUNDLED_LOCALES in .env accepted")
	}
	// Hermes reads config.yaml as YAML 1.1: unquoted yes is a bool, not text.
	for config, refused := range map[string]bool{
		"agent:\n  system_prompt: yes\n":                                    true,
		"agent:\n  system_prompt: 'yes'\n":                                  false,
		"agent:\n  system_prompt: [first, on]\n":                            true,
		"display:\n  personality: on\n":                                     true,
		"display:\n  personality: mine\npersonalities:\n  mine: 0o755\n":    false,
		"display:\n  personality: mine\npersonalities:\n  mine: 1_000\n":    true,
		"agent:\n  system_prompt:\n":                                        false,
		"agent:\n  personalities:\n    other: yes\n  system_prompt: text\n": false,
	} {
		home := t.TempDir()
		write(filepath.Join(home, "config.yaml"), config)
		if err := hooks(home, "", nil); (err != nil) != refused {
			t.Errorf("%q: err = %v, want refused=%v", config, err, refused)
		}
	}
}

func TestResolveHomeAppliesDataDirSuffix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX default home")
	}
	home := t.TempDir()
	got, err := ResolveHome(map[string]string{"HERMES_HOME": "", "HOME": home, "HERMES_DATA_DIR_SUFFIX": "-dev"})
	if err != nil || got != filepath.Join(home, ".hermes-dev") {
		t.Fatalf("ResolveHome = %q, %v", got, err)
	}
}

func TestReleaseBannerRefusesModifiedSourceCheckout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell launcher")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	launcher := filepath.Join(root, ".hermes", "bin", "hermes")
	if err := os.MkdirAll(filepath.Dir(launcher), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\necho 'Hermes Agent v0.22.2 (2026.9.24)'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "en.yaml"), []byte("busy: Working\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("init", "-q", "-b", "main")
	git("add", "en.yaml")
	git("commit", "-q", "-m", "release")
	if ok, why, _ := probeNativeVersion(t.Context(), launcher); !ok {
		t.Fatalf("clean release checkout rejected: %s", why)
	}
	// The release banner has no dirty marker; git does.
	if err := os.WriteFile(filepath.Join(root, "en.yaml"), []byte("busy: REVIEW LOCAL BUSY\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, _, _ := probeNativeVersion(t.Context(), launcher); ok {
		t.Fatal("modified source checkout accepted under a release banner")
	}
}

func TestPluginLanguagePackRoutesMatchNativeDiscovery(t *testing.T) {
	t.Setenv("HERMES_BUNDLED_LOCALES", "")
	t.Setenv("HERMES_BUNDLED_PLUGINS", "")
	hooks := func(home, workspace string, env map[string]string) error {
		if env == nil {
			env = map[string]string{}
		}
		env["HERMES_HOME"] = home
		return New().GetAgentHooks(t.Context(), ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "ao-1", WorkspacePath: workspace, Env: env, SystemPrompt: "AO"})
	}
	pack := func(lang string) string {
		t.Helper()
		dir := t.TempDir()
		for path, content := range map[string]string{
			"plugin.yaml":               "name: review-pack\nversion: 1.0.0\nprovides_locales: [" + lang + "]\n",
			"locales/" + lang + ".yaml": "cli:\n  tui:\n    placeholder_busy: Working - please wait\n",
		} {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	link := func(target, path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	// Native discovery follows a symlinked plugin directory.
	home := t.TempDir()
	link(pack("en"), filepath.Join(home, "plugins", "review-pack"))
	if err := hooks(home, "", nil); err == nil {
		t.Error("symlinked English plugin pack accepted")
	}
	home = t.TempDir()
	link(pack("de"), filepath.Join(home, "plugins", "review-pack"))
	if err := hooks(home, "", nil); err != nil {
		t.Errorf("symlinked German plugin pack refused: %v", err)
	}
	// HERMES_BUNDLED_PLUGINS is another plugin root, relative to the workspace.
	bundled := t.TempDir()
	link(pack("en"), filepath.Join(bundled, "review-pack"))
	if err := hooks(t.TempDir(), "", map[string]string{"HERMES_BUNDLED_PLUGINS": bundled}); err == nil {
		t.Error("English pack under HERMES_BUNDLED_PLUGINS accepted")
	}
	workspace := t.TempDir()
	link(bundled, filepath.Join(workspace, "review-plugins"))
	if err := hooks(t.TempDir(), workspace, map[string]string{"HERMES_BUNDLED_PLUGINS": "review-plugins"}); err == nil {
		t.Error("relative HERMES_BUNDLED_PLUGINS accepted")
	}
	// link/.. resolves through the symlink natively, not lexically.
	workspace, external := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(external, "target"), 0o700); err != nil {
		t.Fatal(err)
	}
	link(pack("en"), filepath.Join(external, "bundled", "review-pack"))
	link(filepath.Join(external, "target"), filepath.Join(workspace, "link"))
	if err := hooks(t.TempDir(), workspace, map[string]string{"HERMES_BUNDLED_PLUGINS": "link/../bundled"}); err == nil {
		t.Error("HERMES_BUNDLED_PLUGINS through link/.. accepted")
	}
	// Unset (t.Setenv above restores it), so a secret source cannot gap-fill it.
	if err := os.Unsetenv("HERMES_BUNDLED_PLUGINS"); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	if err := hooks(t.TempDir(), "", env); err != nil {
		t.Fatal(err)
	}
	if value, pinned := env["HERMES_BUNDLED_PLUGINS"]; !pinned || value != "" {
		t.Errorf("HERMES_BUNDLED_PLUGINS not pinned empty: %q, %v", value, pinned)
	}
	// HERMES_BUNDLED_LOCALES resolves against the session workspace.
	workspace = t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "review-catalog"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := hooks(t.TempDir(), workspace, map[string]string{"HERMES_BUNDLED_LOCALES": "review-catalog"}); err == nil {
		t.Error("workspace-relative HERMES_BUNDLED_LOCALES accepted")
	}
	if err := hooks(t.TempDir(), workspace, map[string]string{"HERMES_BUNDLED_LOCALES": "missing-catalog"}); err != nil {
		t.Errorf("missing HERMES_BUNDLED_LOCALES refused although Hermes ignores it: %v", err)
	}
}

func TestStandingInstructionResidualsFollowNativeConfig(t *testing.T) {
	t.Setenv("HERMES_BUNDLED_LOCALES", "")
	t.Setenv("HERMES_BUNDLED_PLUGINS", "")
	t.Setenv("AO_REVIEW_UNUSED_NAME", "renamed")
	for config, refused := range map[string]bool{
		// Hermes expands values, not keys: an unused literal key selects nothing.
		"display:\n  personality: helpful\npersonalities:\n  \"${AO_REVIEW_UNUSED_NAME}\": Unselected static prompt\n": false,
		"display:\n  personality: \"${AO_REVIEW_UNUSED_NAME}\"\n":                                                      true,
		// Explicit binary renders as b'...' natively.
		"agent:\n  system_prompt: !!binary SGVsbG8=\n": true,
		"agent:\n  system_prompt: \"b'Hello'\"\n":      false,
		"agent:\n  system_prompt: !!str yes\n":         false,
		// One strict-true source opens startup; the rest are fetched on truthiness.
		"secrets:\n  command:\n    enabled: true\n    override_existing: false\n  bitwarden:\n    enabled: 'yes'\n    override_existing: true\n": true,
		"secrets:\n  command:\n    enabled: true\n    override_existing: false\n  bitwarden:\n    enabled: 1\n    override_existing: true\n":     true,
		"secrets:\n  command:\n    enabled: true\n    override_existing: false\n  bitwarden:\n    enabled: 0\n    override_existing: true\n":     false,
		"secrets:\n  command:\n    enabled: true\n    override_existing: false\n  bitwarden:\n    enabled: false\n":                              false,
		"secrets:\n  bitwarden:\n    enabled: 'yes'\n    override_existing: true\n":                                                              false,
	} {
		home := t.TempDir()
		if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
		env := map[string]string{"HERMES_HOME": home}
		err := New().GetAgentHooks(t.Context(), ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "ao-1", Env: env, SystemPrompt: "AO"})
		if (err != nil) != refused {
			t.Errorf("%q: err = %v, want refused=%v", config, err, refused)
		}
	}
}

func TestDefaultHomeThatMovesWhenPinnedIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX default home")
	}
	t.Setenv("HERMES_BUNDLED_LOCALES", "")
	t.Setenv("HERMES_BUNDLED_PLUGINS", "")
	// A reference to a variable AO sets later still expands in the child.
	t.Setenv("HERMES_LANGUAGE", "")
	if err := os.Unsetenv("HERMES_LANGUAGE"); err != nil {
		t.Fatal(err)
	}
	for suffix, refused := range map[string]bool{"-dev": false, "-dev ": true, "-$USER": true, "$HERMES_LANGUAGE": true, "${HERMES_LANGUAGE}": true} {
		env := map[string]string{"HERMES_HOME": "", "HOME": t.TempDir(), "HERMES_DATA_DIR_SUFFIX": suffix}
		err := New().GetAgentHooks(t.Context(), ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "ao-1", Env: env, SystemPrompt: "AO"})
		if (err != nil) != refused {
			t.Errorf("suffix %q: err = %v, want refused=%v", suffix, err, refused)
		}
	}
}

func TestSourceCleanlinessIgnoresInheritedGitContextAndCache(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell launcher")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root, clean := t.TempDir(), t.TempDir()
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	launcher := filepath.Join(root, ".hermes", "bin", "hermes")
	if err := os.MkdirAll(filepath.Dir(launcher), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\necho 'Hermes Agent v0.22.2 (2026.9.24)'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "en.yaml"), []byte("busy: Working\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(root, "init", "-q", "-b", "main")
	git(root, "add", "en.yaml")
	git(root, "commit", "-q", "-m", "release")
	git(clean, "clone", "-q", root, ".")
	if ok, why, _ := probeNativeVersion(t.Context(), launcher); !ok {
		t.Fatalf("clean release checkout rejected: %s", why)
	}
	rememberIdentity(launcher)
	t.Cleanup(func() { forgetIdentity(launcher) })
	if !identityConfirmed(t.Context(), launcher) {
		t.Fatal("fresh confirmation not reused")
	}
	if err := os.WriteFile(filepath.Join(root, "en.yaml"), []byte("busy: REVIEW LOCAL BUSY\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A tracked edit changes neither the launcher nor HEAD.
	if identityConfirmed(t.Context(), launcher) {
		t.Fatal("cached confirmation reused for a modified source checkout")
	}
	if err := os.WriteFile(filepath.Join(root, "en.yaml"), []byte("busy: Working\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// An untracked or ignored English catalog in the checkout's bundled
	// locales or plugins is loaded natively without touching tracked files.
	for _, rel := range []string{"locales/en.tui.yaml", "plugins/pack/locales/en.yaml"} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("cli: {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if ok, _, _ := probeNativeVersion(t.Context(), launcher); ok {
			t.Errorf("untracked %s accepted", rel)
		}
		if err := os.WriteFile(filepath.Join(root, ".git", "info", "exclude"), []byte(rel+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if ok, _, _ := probeNativeVersion(t.Context(), launcher); ok {
			t.Errorf("ignored %s accepted", rel)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "locales", "de.yaml"), []byte("cli: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, why, _ := probeNativeVersion(t.Context(), launcher); !ok {
		t.Fatalf("untracked non-English catalog refused: %s", why)
	}
	if err := os.WriteFile(filepath.Join(root, "en.yaml"), []byte("busy: REVIEW LOCAL BUSY\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// AO's own git context must not supply another tree's cleanliness.
	t.Setenv("GIT_WORK_TREE", clean)
	t.Setenv("GIT_DIR", filepath.Join(root, ".git"))
	if ok, _, _ := probeNativeVersion(t.Context(), launcher); ok {
		t.Fatal("inherited GIT_WORK_TREE made a modified checkout look clean")
	}
}

func TestSourceCatalogPathsInvalidateFreshAndCachedQualification(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell launcher and filenames")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	for _, quotePath := range []string{"true", "false"} {
		t.Run("quotePath="+quotePath, func(t *testing.T) {
			root := t.TempDir()
			git := func(args ...string) string {
				t.Helper()
				cmd := exec.Command("git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)...)
				cmd.Dir = root
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
				return strings.TrimSpace(string(out))
			}
			git("init", "-q", "-b", "main")
			git("config", "core.quotePath", quotePath)
			git("commit", "-q", "--allow-empty", "-m", "release")
			git("tag", "v0.22.2")
			head := git("rev-parse", "HEAD")
			banner := "Hermes Agent vgit." + head[:7]
			launcher := filepath.Join(root, ".hermes", "bin", "hermes")
			if err := os.MkdirAll(filepath.Dir(launcher), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(launcher, []byte("#!/bin/sh\necho 'Hermes Agent v0.22.2'\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { forgetIdentity(launcher) })
			for _, tt := range []struct {
				name, path string
				modified   bool
			}{
				{"ascii", "plugins/pack/locales/en.yaml", true},
				{"unicode", "plugins/日本語/locales/en.yaml", true},
				{"quote", "plugins/pack\"quoted/locales/en.tui.yaml", true},
				{"tab", "plugins/pack\tname/locales/en.core.yml", true},
				{"newline", "plugins/pack\nname/locales/en.desktop.yaml", true},
				{"spaces", "plugins/ pack /locales/en.yaml", true},
				{"non-English", "plugins/日本語/locales/de.yaml", false},
			} {
				t.Run(tt.name, func(t *testing.T) {
					// Prime the same identity while the source is still clean.
					if ok, why, _ := probeNativeVersion(t.Context(), launcher); !ok {
						t.Fatalf("clean checkout rejected: %s", why)
					}
					rememberIdentity(launcher)
					if !identityConfirmed(t.Context(), launcher) {
						t.Fatal("clean confirmation not reused")
					}
					path := filepath.Join(root, tt.path)
					if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte("cli:\n  tui:\n    placeholder_busy: REVIEW WORKING\n"), 0o600); err != nil {
						t.Fatal(err)
					}
					for _, ignored := range []bool{false, true} {
						if ignored {
							// Ignore the root so filenames with newlines are covered too.
							if err := os.WriteFile(filepath.Join(root, ".git", "info", "exclude"), []byte("/plugins/\n"), 0o600); err != nil {
								t.Fatal(err)
							}
						}
						if modified, known := sourceModified(t.Context(), root); !known || modified != tt.modified {
							t.Errorf("ignored=%v: sourceModified = %v, %v; want %v, true", ignored, modified, known, tt.modified)
						}
						if ok, why, _ := probeNativeVersion(t.Context(), launcher); ok == tt.modified {
							t.Errorf("ignored=%v: fresh qualification = %v, %s; want %v", ignored, ok, why, !tt.modified)
						}
						if got := identityConfirmed(t.Context(), launcher); got == tt.modified {
							t.Errorf("ignored=%v: cached qualification = %v; want %v", ignored, got, !tt.modified)
						}
						wantTag := "v0.22.2"
						if tt.modified {
							wantTag = ""
						}
						if tag := SourceReleaseTag(t.Context(), launcher, banner); tag != wantTag {
							t.Errorf("ignored=%v: source release = %q; want %q", ignored, tag, wantTag)
						}
					}
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(root, ".git", "info", "exclude"), nil, 0o600); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}
}

func TestStructuredPersonalityRendersLikeNative(t *testing.T) {
	t.Setenv("HERMES_BUNDLED_LOCALES", "")
	t.Setenv("HERMES_BUNDLED_PLUGINS", "")
	const prefix = "display:\n  personality: mine\npersonalities:\n  mine:\n"
	for fields, want := range map[string]string{
		// str() of a list or map is Python repr, which AO refuses to guess.
		"    system_prompt: [First, Second]\n":                     "",
		"    system_prompt: []\n":                                  "",
		"    system_prompt: Text\n    tone: [Precise, Short]\n":    "",
		"    system_prompt: null\n":                                "None\n\nAO",
		"    system_prompt: Text\n":                                "Text\n\nAO",
		"    system_prompt: Text\n    tone: ''\n    style: null\n": "Text\n\nAO",
		"    system_prompt: Text\n    tone: '  Precise  '\n":       "Text\nTone:   Precise\n\nAO",
		"    tone: Precise\n":                                      "Tone: Precise\n\nAO",
	} {
		home := t.TempDir()
		if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(prefix+fields), 0o600); err != nil {
			t.Fatal(err)
		}
		env := map[string]string{"HERMES_HOME": home}
		err := New().GetAgentHooks(t.Context(), ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "ao-1", Env: env, SystemPrompt: "AO"})
		if want == "" {
			if err == nil {
				t.Errorf("%q accepted with overlay %q", fields, env["HERMES_EPHEMERAL_SYSTEM_PROMPT"])
			}
			continue
		}
		if err != nil || env["HERMES_EPHEMERAL_SYSTEM_PROMPT"] != want {
			t.Errorf("%q: overlay %q, %v; want %q", fields, env["HERMES_EPHEMERAL_SYSTEM_PROMPT"], err, want)
		}
	}
}

func TestNativeHomeWithParentComponentIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlink layout")
	}
	t.Setenv("HERMES_BUNDLED_LOCALES", "")
	t.Setenv("HERMES_BUNDLED_PLUGINS", "")
	base, target := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(target, "deep"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(target, "native"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(target, "deep"), filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	hooks := func(env map[string]string) error {
		return New().GetAgentHooks(t.Context(), ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "ao-1", Env: env, SystemPrompt: "AO"})
	}
	// Natively link/.. is target, not base.
	if err := hooks(map[string]string{"HERMES_HOME": base + "/link/../native"}); err == nil {
		t.Error("explicit home through link/.. accepted")
	}
	if err := hooks(map[string]string{"HERMES_HOME": "", "HOME": t.TempDir(), "HERMES_DATA_DIR_SUFFIX": "/../native"}); err == nil {
		t.Error("data-dir suffix with .. accepted")
	}
	// An ordinary symlinked home is preserved.
	if err := os.Symlink(filepath.Join(target, "native"), filepath.Join(base, "home")); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HERMES_HOME": filepath.Join(base, "home")}
	if err := hooks(env); err != nil || env["HERMES_HOME"] != filepath.Join(base, "home") {
		t.Errorf("symlinked home: %q, %v", env["HERMES_HOME"], err)
	}
}
