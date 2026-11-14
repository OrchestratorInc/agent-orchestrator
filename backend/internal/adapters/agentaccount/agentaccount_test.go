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
	"time"

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

func TestClaudePrepareSharesHistoryNotLogin(t *testing.T) {
	device, home := t.TempDir(), filepath.Join(t.TempDir(), "acct")
	write(t, filepath.Join(device, "projects", "-repo", "s1.jsonl"), "{}\n")
	write(t, filepath.Join(device, "CLAUDE.md"), "rules\n")
	write(t, filepath.Join(device, "settings.json"), `{"model":"opus","apiKeyHelper":"x","env":{"ANTHROPIC_API_KEY":"k","FOO":"1"}}`)
	write(t, filepath.Join(device, ".credentials.json"), `{"secret":true}`)
	config := filepath.Join(t.TempDir(), ".claude.json")
	write(t, config, `{"mcpServers":{"m":{"command":"x"}},"theme":"light","oauthAccount":{"emailAddress":"a@example.com"}}`)
	if err := os.MkdirAll(filepath.Join(home, "todos"), 0o700); err != nil {
		t.Fatal(err)
	}

	c := &Claude{DeviceHome: device, DeviceConfig: config}
	for range 2 {
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

func TestClaudePrepareKeepsChangesMadeOnTheAccount(t *testing.T) {
	device, home := t.TempDir(), filepath.Join(t.TempDir(), "acct")
	config := filepath.Join(t.TempDir(), ".claude.json")
	write(t, filepath.Join(device, "settings.json"), `{"model":"opus","verbose":false,"cleanupPeriodDays":30,"env":{"FOO":"1","BAR":"1"}}`)
	write(t, config, `{"mcpServers":{"m":{"command":"x"}},"theme":"light"}`)
	c := &Claude{DeviceHome: device, DeviceConfig: config}
	if err := c.Prepare(home); err != nil {
		t.Fatal(err)
	}

	write(t, filepath.Join(home, "settings.json"), `{"model":"haiku","verbose":false,"cleanupPeriodDays":30,"env":{"FOO":"1","BAR":"mine"},"statusLine":"s"}`)
	write(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"m":{"command":"x"},"mine":{"command":"y"}},"theme":"light","oauthAccount":{"emailAddress":"b@example.com"},"hasCompletedOnboarding":true}`)
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

func TestCodexPrepareKeepsLoginAndDaemonPrivate(t *testing.T) {
	device, home := t.TempDir(), filepath.Join(t.TempDir(), "acct")
	write(t, filepath.Join(device, "auth.json"), `{"secret":true}`)
	write(t, filepath.Join(device, "config.toml"), "model = \"gpt-5\"\n")
	write(t, filepath.Join(device, "sessions", "2026", "r.jsonl"), "{}\n")
	write(t, filepath.Join(device, "app-server-daemon", "pid"), "1")
	write(t, filepath.Join(device, "state_5.sqlite"), "")
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

func TestClaudeDeviceIdentitySeesSavedSetupToken(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the CLI")
	}
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	bin := filepath.Join(t.TempDir(), "claude")
	write(t, bin, "#!/bin/sh\nif [ -n \"$CLAUDE_CODE_OAUTH_TOKEN\" ]; then printf '{\"loggedIn\":true,\"authMethod\":\"oauth_token\"}'; else printf '{\"loggedIn\":false,\"authMethod\":\"none\"}'; fi\n")
	if err := os.Chmod(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	c := &Claude{
		Binary: func(context.Context) (string, error) { return bin, nil },
		DeviceEnv: func(context.Context) map[string]string {
			return map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "sk-ant-oat01-saved"}
		},
	}
	device, err := c.Identity(context.Background(), "")
	if err != nil || !device.SignedIn {
		t.Fatalf("device identity = %+v, %v, want signed in with the saved token", device, err)
	}
	account, err := c.Identity(context.Background(), t.TempDir())
	if err != nil || account.LoggedIn {
		t.Fatalf("account identity = %+v, %v, want the folder's own (signed-out) login", account, err)
	}
}

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
		name, status        string
		signedIn, loggedIn  bool
		email, org, orgName string
	}{
		{"claude.ai", `{"loggedIn":true,"authMethod":"claude.ai","email":"a@example.com","subscriptionType":"max","orgId":"org-1","orgName":"Team"}`, true, true, "a@example.com", "org-1", "Team"},
		{"token", `{"loggedIn":true,"authMethod":"oauth_token"}`, true, true, "", "", ""},
		{"api key", `{"loggedIn":true,"authMethod":"api_key"}`, false, true, "", "", ""},
		{"signed out", `{"loggedIn":false,"authMethod":"none"}`, false, false, "", "", ""},
	} {
		t.Setenv("FAKE_CLAUDE_STATUS", tc.status)
		got, err := c.Identity(context.Background(), "")
		if err != nil || got.SignedIn != tc.signedIn || got.LoggedIn != tc.loggedIn || got.Email != tc.email || got.OrgID != tc.org || got.OrgName != tc.orgName {
			t.Fatalf("%s: identity = %+v, %v", tc.name, got, err)
		}
	}
}

func TestClaudeSlowCheckReportsTheDeadline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the CLI")
	}
	bin := filepath.Join(t.TempDir(), "claude")
	write(t, bin, "#!/bin/sh\nexec sleep 10\n")
	if err := os.Chmod(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	c := &Claude{Binary: func(context.Context) (string, error) { return bin, nil }}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := c.Identity(ctx, t.TempDir()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("slow identity check = %v, want the deadline", err)
	}
}
