package agentaccount

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func linksTo(t *testing.T, link, target string) {
	t.Helper()
	got, err := os.Readlink(link)
	if err != nil || got != target {
		t.Fatalf("%s -> %q (%v), want %q", link, got, err, target)
	}
}

func absent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("%s exists, want it private to the device", path)
	}
}

// TestClaudePrepareSharesHistoryNotLogin pins what makes a native resume work
// across accounts without one account's login leaking into another: history is
// a link, the settings copy loses login-picking keys, and the global config
// carries MCP servers but never the device's own account.
func TestClaudePrepareSharesHistoryNotLogin(t *testing.T) {
	device, home := t.TempDir(), filepath.Join(t.TempDir(), "acct")
	write(t, filepath.Join(device, "projects", "-repo", "s1.jsonl"), "{}\n")
	write(t, filepath.Join(device, "CLAUDE.md"), "rules\n")
	write(t, filepath.Join(device, "settings.json"), `{"model":"opus","apiKeyHelper":"x","env":{"ANTHROPIC_API_KEY":"k","FOO":"1"}}`)
	write(t, filepath.Join(device, ".credentials.json"), `{"secret":true}`)
	config := filepath.Join(t.TempDir(), ".claude.json")
	write(t, config, `{"mcpServers":{"m":{"command":"x"}},"theme":"light","oauthAccount":{"emailAddress":"a@example.com"}}`)
	// The CLI made an empty folder before the link did.
	if err := os.MkdirAll(filepath.Join(home, "todos"), 0o700); err != nil {
		t.Fatal(err)
	}

	c := &Claude{DeviceHome: device, DeviceConfig: config}
	for range 2 { // idempotent: every launch runs it
		if err := c.Prepare(home); err != nil {
			t.Fatal(err)
		}
	}
	linksTo(t, filepath.Join(home, "projects"), filepath.Join(device, "projects"))
	linksTo(t, filepath.Join(home, "todos"), filepath.Join(device, "todos"))
	linksTo(t, filepath.Join(home, "CLAUDE.md"), filepath.Join(device, "CLAUDE.md"))
	linksTo(t, filepath.Join(home, "history.jsonl"), filepath.Join(device, "history.jsonl"))
	if _, err := os.Stat(filepath.Join(home, "projects", "-repo", "s1.jsonl")); err != nil {
		t.Fatalf("device transcript not visible: %v", err)
	}
	absent(t, filepath.Join(home, ".credentials.json"))

	var settings map[string]any
	data, _ := os.ReadFile(filepath.Join(home, "settings.json"))
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	env, _ := settings["env"].(map[string]any)
	if settings["model"] != "opus" || settings["apiKeyHelper"] != nil || env["ANTHROPIC_API_KEY"] != nil || env["FOO"] != "1" {
		t.Fatalf("settings copy = %s", data)
	}
	var global map[string]any
	data, _ = os.ReadFile(filepath.Join(home, ".claude.json"))
	if err := json.Unmarshal(data, &global); err != nil {
		t.Fatal(err)
	}
	if global["mcpServers"] == nil || global["theme"] != "light" || global["oauthAccount"] != nil || global["hasCompletedOnboarding"] != true {
		t.Fatalf("global config = %s", data)
	}
}

// TestClaudePrepareKeepsChangesMadeOnTheAccount pins the ledger rule: a shared
// key follows the computer until it is changed on the account, and then it is
// the account's. Entries of env and mcpServers are shared one by one.
func TestClaudePrepareKeepsChangesMadeOnTheAccount(t *testing.T) {
	device, home := t.TempDir(), filepath.Join(t.TempDir(), "acct")
	config := filepath.Join(t.TempDir(), ".claude.json")
	write(t, filepath.Join(device, "settings.json"), `{"model":"opus","verbose":false,"cleanupPeriodDays":30,"env":{"FOO":"1","BAR":"1"}}`)
	write(t, config, `{"mcpServers":{"m":{"command":"x"}},"theme":"light"}`)
	c := &Claude{DeviceHome: device, DeviceConfig: config}
	if err := c.Prepare(home); err != nil {
		t.Fatal(err)
	}

	// On the account: the model and one env entry change, an MCP server is
	// added with `claude mcp add --scope user`, and the sign-in is saved.
	write(t, filepath.Join(home, "settings.json"), `{"model":"haiku","verbose":false,"cleanupPeriodDays":30,"env":{"FOO":"1","BAR":"mine"},"statusLine":"s"}`)
	write(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"m":{"command":"x"},"mine":{"command":"y"}},"theme":"light","oauthAccount":{"emailAddress":"b@example.com"},"hasCompletedOnboarding":true}`)
	// On the computer: every shared value changes, one key goes, a server is added.
	write(t, filepath.Join(device, "settings.json"), `{"model":"sonnet","verbose":true,"env":{"FOO":"2","BAR":"2"}}`)
	write(t, config, `{"mcpServers":{"m":{"command":"x2"},"new":{"command":"z"}},"theme":"dark"}`)
	if err := c.Prepare(home); err != nil {
		t.Fatal(err)
	}

	var settings struct {
		Model             string            `json:"model"`
		Verbose           bool              `json:"verbose"`
		CleanupPeriodDays *int              `json:"cleanupPeriodDays"`
		StatusLine        string            `json:"statusLine"`
		Env               map[string]string `json:"env"`
	}
	data, _ := os.ReadFile(filepath.Join(home, "settings.json"))
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Model != "haiku" || !settings.Verbose || settings.CleanupPeriodDays != nil || settings.StatusLine != "s" ||
		settings.Env["FOO"] != "2" || settings.Env["BAR"] != "mine" {
		t.Fatalf("settings = %s", data)
	}
	var global struct {
		MCPServers   map[string]map[string]string `json:"mcpServers"`
		Theme        string                       `json:"theme"`
		OAuthAccount map[string]string            `json:"oauthAccount"`
	}
	data, _ = os.ReadFile(filepath.Join(home, ".claude.json"))
	if err := json.Unmarshal(data, &global); err != nil {
		t.Fatal(err)
	}
	if global.MCPServers["m"]["command"] != "x2" || global.MCPServers["new"]["command"] != "z" || global.MCPServers["mine"]["command"] != "y" ||
		global.Theme != "dark" || global.OAuthAccount["emailAddress"] != "b@example.com" {
		t.Fatalf("global config = %s", data)
	}

	// A change set back to the computer's value, and a shared key deleted on
	// the account, follow the computer again.
	write(t, filepath.Join(home, "settings.json"), `{"model":"sonnet","env":{"FOO":"2","BAR":"mine"}}`)
	if err := c.Prepare(home); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(device, "settings.json"), `{"model":"fable","verbose":true,"env":{"FOO":"2","BAR":"2"}}`)
	if err := c.Prepare(home); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(home, "settings.json"))
	settings.StatusLine = ""
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Model != "fable" || !settings.Verbose {
		t.Fatalf("settings after undo = %s", data)
	}
}

// TestPrepareRefusesWhenItCannotLink pins that a folder whose history cannot be
// linked is refused, not run with history of its own.
func TestPrepareRefusesWhenItCannotLink(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a folder this user cannot write")
	}
	for name, prepare := range map[string]func(string) error{
		"claude": (&Claude{DeviceHome: t.TempDir(), DeviceConfig: filepath.Join(t.TempDir(), ".claude.json")}).Prepare,
		"codex":  (&Codex{DeviceHome: t.TempDir()}).Prepare,
	} {
		home := filepath.Join(t.TempDir(), "acct")
		if err := os.Mkdir(home, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(home, 0o700) })
		if err := prepare(home); !errors.Is(err, ports.ErrAgentAccountUnshared) {
			t.Fatalf("%s: Prepare = %v, want ErrAgentAccountUnshared", name, err)
		}
	}
}

// TestCodexPrepareKeepsLoginAndDaemonPrivate pins the shadow home: everything
// is shared except the login and the app-server daemon's control files, so a
// terminal on one account cannot attach to a daemon running as another.
func TestCodexPrepareKeepsLoginAndDaemonPrivate(t *testing.T) {
	device, home := t.TempDir(), filepath.Join(t.TempDir(), "acct")
	write(t, filepath.Join(device, "auth.json"), `{"secret":true}`)
	write(t, filepath.Join(device, "config.toml"), "model = \"gpt-5\"\n")
	write(t, filepath.Join(device, "sessions", "2026", "r.jsonl"), "{}\n")
	write(t, filepath.Join(device, "app-server-daemon", "pid"), "1")
	write(t, filepath.Join(device, "state_5.sqlite"), "")
	// Codex made its own lock folder in the fresh home first.
	write(t, filepath.Join(home, "mcp-oauth-locks", "l"), "")

	c := &Codex{DeviceHome: device}
	for range 2 {
		if err := c.Prepare(home); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"config.toml", "sessions", "state_5.sqlite", "thread-writer-locks", "mcp-oauth-locks", "archived_sessions"} {
		linksTo(t, filepath.Join(home, name), filepath.Join(device, name))
	}
	absent(t, filepath.Join(home, "auth.json"))
	absent(t, filepath.Join(home, "app-server-daemon"))
}

// A fresh computer may not have the files Codex writes on its first run. The
// account's first launch must still share them, and an account that already
// made its own (before they were shared) gives its copy to the computer.
func TestCodexPrepareSharesFilesTheComputerDoesNotHaveYet(t *testing.T) {
	device, home := t.TempDir(), filepath.Join(t.TempDir(), "acct")
	write(t, filepath.Join(home, "history.jsonl"), "{\"text\":\"asked on the account\"}\n")

	c := &Codex{DeviceHome: device}
	if err := c.Prepare(home); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"config.toml", "history.jsonl"} {
		linksTo(t, filepath.Join(home, name), filepath.Join(device, name))
	}
	if data, err := os.ReadFile(filepath.Join(device, "history.jsonl")); err != nil || !strings.Contains(string(data), "asked on the account") {
		t.Fatalf("the account's own history was not kept as the shared one: %q %v", data, err)
	}
}

// A link to an entry the user deleted from the computer's own folder is
// removed; a link to an entry AO always shares is restored.
func TestPrepareDropsLinksToDeletedEntries(t *testing.T) {
	device, home := t.TempDir(), filepath.Join(t.TempDir(), "acct")
	write(t, filepath.Join(device, "rules", "r.md"), "x")
	c := &Codex{DeviceHome: device}
	if err := c.Prepare(home); err != nil {
		t.Fatal(err)
	}
	linksTo(t, filepath.Join(home, "rules"), filepath.Join(device, "rules"))
	if err := os.RemoveAll(filepath.Join(device, "rules")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(device, "sessions")); err != nil {
		t.Fatal(err)
	}
	if err := c.Prepare(home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(home, "rules")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("link to a deleted entry kept: %v", err)
	}
	linksTo(t, filepath.Join(home, "sessions"), filepath.Join(device, "sessions"))
	if _, err := os.Stat(filepath.Join(home, "sessions")); err != nil {
		t.Fatalf("shared sessions not restored: %v", err)
	}
}

// TestLaunchEnvOverridesCredentialEnv pins the right-account guarantee: a key
// in the daemon's environment cannot outrank the account's own login.
func TestLaunchEnvOverridesCredentialEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "device-key")
	t.Setenv("OPENAI_API_KEY", "device-key")
	claude := (&Claude{}).LaunchEnv("/acct")
	if claude["CLAUDE_CONFIG_DIR"] != "/acct" || claude["CLAUDE_SECURESTORAGE_CONFIG_DIR"] != "/acct" {
		t.Fatalf("claude env = %v", claude)
	}
	if value, set := claude["ANTHROPIC_API_KEY"]; !set || value != "" {
		t.Fatalf("ANTHROPIC_API_KEY not cleared: %v", claude)
	}
	if _, set := claude["CLAUDE_CODE_OAUTH_TOKEN"]; set {
		t.Fatalf("unset variable added: %v", claude)
	}
	codex := (&Codex{}).LaunchEnv("/acct")
	if codex["CODEX_HOME"] != "/acct" || codex["OPENAI_API_KEY"] != "" {
		t.Fatalf("codex env = %v", codex)
	}
	if (&Claude{}).LaunchEnv("") != nil || (&Codex{}).LaunchEnv("") != nil {
		t.Fatal("the device account must launch with the environment untouched")
	}
}

// TestClaudeIdentityCountsSubscriptionLogins pins which `claude auth status`
// reports are signed in: a claude.ai login, and a CLAUDE_CODE_OAUTH_TOKEN login
// (the long-lived subscription token from `claude setup-token`), which carries
// no email. An API key is not a subscription sign-in.
func TestClaudeIdentityCountsSubscriptionLogins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the CLI")
	}
	bin := filepath.Join(t.TempDir(), "claude")
	write(t, bin, "#!/bin/sh\nprintf '%s' \"$FAKE_CLAUDE_STATUS\"\n")
	if err := os.Chmod(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	c := &Claude{Binary: func(context.Context) (string, error) { return bin, nil }}
	for _, tc := range []struct {
		name, status       string
		signedIn, loggedIn bool
		email              string
	}{
		{"claude.ai", `{"loggedIn":true,"authMethod":"claude.ai","email":"a@example.com","subscriptionType":"max"}`, true, true, "a@example.com"},
		{"token", `{"loggedIn":true,"authMethod":"oauth_token"}`, true, true, ""},
		// Not a subscription, but the CLI can launch with it.
		{"api key", `{"loggedIn":true,"authMethod":"api_key"}`, false, true, ""},
		{"signed out", `{"loggedIn":false,"authMethod":"none"}`, false, false, ""},
	} {
		t.Setenv("FAKE_CLAUDE_STATUS", tc.status)
		got, err := c.Identity(context.Background(), "")
		if err != nil || got.SignedIn != tc.signedIn || got.LoggedIn != tc.loggedIn || got.Email != tc.email {
			t.Fatalf("%s: identity = %+v, %v", tc.name, got, err)
		}
	}
}
