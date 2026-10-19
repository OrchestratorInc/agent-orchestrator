package hermes

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestResolveBinaryReportsLinuxOnlyPlatform(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("Linux runs the native resolver")
	}
	_, err := New().ResolveBinary(context.Background())
	if !errors.Is(err, ports.ErrAgentBinaryLinuxOnly) {
		t.Fatalf("ResolveBinary() error = %v, want Linux-only sentinel", err)
	}
}

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
func TestRestoreRefusesUnobservedIdentity(t *testing.T) {
	for _, id := range []string{"", "latest", "some title", "../../state.db"} {
		_, ok, err := New().GetRestoreCommand(context.Background(), ports.RestoreConfig{Session: ports.SessionRef{Metadata: map[string]string{ports.MetadataKeyAgentSessionID: id}}})
		if ok || err == nil {
			t.Fatalf("id %q: ok=%v err=%v", id, ok, err)
		}
	}
}
func TestLaunchRejectsUnsupportedPermissionsAndRestrictions(t *testing.T) {
	for _, cfg := range []ports.LaunchConfig{{Permissions: ports.PermissionModeAcceptEdits}, {Permissions: ports.PermissionModeAuto}, {AllowedTools: []string{"read"}}} {
		if _, err := New().GetLaunchCommand(context.Background(), cfg); err == nil {
			t.Fatal("unsupported control accepted")
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

func TestNativeVersionRequiresExactFirstLine(t *testing.T) {
	// The official pinned checkout includes the date/source suffix in --version.
	for _, line := range []string{"Hermes Agent v0.21.6", "Hermes Agent v0.21.6 (2026.9.24) · upstream 818c13be"} {
		if !nativeVersionLine.MatchString(line) {
			t.Fatalf("rejected pinned native banner %q", line)
		}
	}

	for _, output := range []string{"Hermes Agent v0.21.60", "Hermes Agent v0.21.6-malicious", "wrapper\nHermes Agent v0.21.6", "Hermes Agent v0.21.6 (2026.9.24) · upstream deadbeef", "Hermes Agent v0.21.6 (2026.9.24) · upstream 818c13be extra"} {
		first, _, _ := strings.Cut(output, "\n")
		if nativeVersionLine.MatchString(first) {
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
	const maxEnvString = 128 * 1024
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

func TestLeadingDashTaskStaysInPrivateQueryFile(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Hermes native lease observation supports Linux only")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "hermes"), []byte("#!/bin/sh\necho 'Hermes Agent v0.21.6'\n"), 0755); err != nil {
		t.Fatal(err)
	}
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
	if runtime.GOOS != "linux" {
		t.Skip("Hermes native lease observation supports Linux only")
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

func TestRestoreRefusesNativeRotationAfterObservation(t *testing.T) {
	data := t.TempDir()
	dir := sessionDir(data, "ao-rotation")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "native-home"), []byte(home), 0o600); err != nil {
		t.Fatal(err)
	}
	crumb := filepath.Join(home, "native-terminal-fixture")
	if err := os.WriteFile(crumb, []byte(`{"session_id":"new-conversation","cwd":"/workspace","ts":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "observed-terminal"), []byte(crumb), 0o600); err != nil {
		t.Fatal(err)
	}
	_, ok, err := New().GetRestoreCommand(context.Background(), ports.RestoreConfig{DataDir: data, Session: ports.SessionRef{ID: "ao-rotation", WorkspacePath: "/workspace", Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "old-conversation"}}})
	if err == nil || ok || !strings.Contains(err.Error(), "rotated") {
		t.Fatalf("stale conversation restore: %v %v", ok, err)
	}
}

func TestRestoreReportsNativeDatabaseFailureSeparately(t *testing.T) {
	data, home := t.TempDir(), t.TempDir()
	dir := sessionDir(data, "ao-db-error")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	crumb := filepath.Join(home, "terminal")
	for path, value := range map[string]string{
		filepath.Join(dir, "native-home"):       home,
		filepath.Join(dir, "observed-terminal"): crumb,
		crumb:                                   `{"session_id":"native-id","cwd":"/workspace","ts":1}`,
		filepath.Join(home, "state.db"):         "not a sqlite database",
	} {
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, ok, err := New().GetRestoreCommand(t.Context(), ports.RestoreConfig{DataDir: data, Session: ports.SessionRef{ID: "ao-db-error", WorkspacePath: "/workspace", Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "native-id"}}})
	if ok || err == nil || !strings.Contains(err.Error(), "native state database unavailable") {
		t.Fatalf("database failure disguised as missing history: ok=%v err=%v", ok, err)
	}
}

func TestUnattendedReleaseRefreshRemainsGated(t *testing.T) {
	var capability ports.AgentTerminalVersionRefresher = New()
	if capability.SupportsTerminalVersionRefresh() {
		t.Fatal("pinned Hermes native identity does not qualify unattended release changes")
	}
}
