package sessionmanager

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agentaccount"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/harnessendpoint"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/providerkeys"
)

func TestRuntimeEnvAppliesCustomHarnessEndpoint(t *testing.T) {
	dir := t.TempDir()
	if err := providerkeys.Save(t.Context(), dir, "ANTHROPIC_API_KEY", "ao-saved"); err != nil {
		t.Fatal(err)
	}
	if _, err := harnessendpoint.ForDataDir(dir).Save("claude-code", harnessendpoint.Input{BaseURL: "https://gateway.example.com", APIKey: "sk-endpoint", Model: "glm-4.6"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		dataDir:    dir,
		executable: func() (string, error) { return filepath.Join(dir, "ao"), nil },
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	claude, err := manager.runtimeEnvChecked("session", "project", "", nil, "", "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if claude["ANTHROPIC_BASE_URL"] != "https://gateway.example.com" || claude["ANTHROPIC_AUTH_TOKEN"] != "sk-endpoint" || claude["ANTHROPIC_MODEL"] != "glm-4.6" {
		t.Fatalf("custom endpoint not applied: %v", claude)
	}
	if value, ok := claude["ANTHROPIC_API_KEY"]; !ok || value != "" {
		t.Fatalf("ANTHROPIC_API_KEY = %q (set=%v), want blanked beside the endpoint token", value, ok)
	}

	project, err := manager.runtimeEnvChecked("session", "project", "", map[string]string{"ANTHROPIC_MODEL": "project-model"}, "", "claude-code")
	if err != nil || project["ANTHROPIC_MODEL"] != "project-model" {
		t.Fatalf("project env must win: model=%q err=%v", project["ANTHROPIC_MODEL"], err)
	}

	goose, err := manager.runtimeEnvChecked("session", "project", "", nil, "", "goose")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := goose["ANTHROPIC_BASE_URL"]; ok || goose["ANTHROPIC_API_KEY"] != "ao-saved" {
		t.Fatalf("endpoint leaked into another harness or replaced its key: %v", goose)
	}
}

// savedAPIKeyAccount is a session bound to a saved Anthropic API-key account.
type savedAPIKeyAccount struct {
	ports.AgentAccountLaunchEnv
}

func (savedAPIKeyAccount) SessionLaunchEnv(context.Context, domain.SessionID) (map[string]string, error) {
	return map[string]string{"ANTHROPIC_API_KEY": "sk-account", "ANTHROPIC_BASE_URL": "https://api.anthropic.com"}, nil
}

func (a savedAPIKeyAccount) SessionLaunchEnvWith(ctx context.Context, id domain.SessionID, _ string) (map[string]string, error) {
	return a.SessionLaunchEnv(ctx, id)
}

// A bound account's saved key must never reach the custom endpoint: the base
// URL and key both come from the endpoint, whichever auth style it uses.
func TestRuntimeEnvKeepsAccountKeyAwayFromCustomEndpoint(t *testing.T) {
	for _, style := range []harnessendpoint.AuthStyle{harnessendpoint.AuthBearer, harnessendpoint.AuthAPIKey} {
		dir := t.TempDir()
		if _, err := harnessendpoint.ForDataDir(dir).Save("claude-code", harnessendpoint.Input{BaseURL: "https://gateway.example.com", AuthStyle: style, APIKey: "sk-endpoint"}, time.Now()); err != nil {
			t.Fatal(err)
		}
		manager := &Manager{
			dataDir:       dir,
			executable:    func() (string, error) { return filepath.Join(dir, "ao"), nil },
			logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
			agentAccounts: savedAPIKeyAccount{},
		}
		env, err := manager.runtimeEnvChecked("session", "project", "", nil, "", "claude-code")
		if err != nil {
			t.Fatal(err)
		}
		if env["ANTHROPIC_BASE_URL"] != "https://gateway.example.com" {
			t.Fatalf("%s: base URL = %q", style, env["ANTHROPIC_BASE_URL"])
		}
		for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"} {
			if env[key] == "sk-account" {
				t.Fatalf("%s: account key reached the endpoint via %s: %v", style, key, env)
			}
		}
		// A same-origin project URL keeps its value through the account step.
		const projectURL = "https://gateway.example.com/v2"
		project, err := manager.runtimeEnvChecked("session", "project", "", map[string]string{"ANTHROPIC_BASE_URL": projectURL}, "", "claude-code")
		if err != nil || project["ANTHROPIC_BASE_URL"] != projectURL {
			t.Fatalf("%s: project base URL = %q, err=%v", style, project["ANTHROPIC_BASE_URL"], err)
		}
		keyVar := map[harnessendpoint.AuthStyle]string{harnessendpoint.AuthBearer: "ANTHROPIC_AUTH_TOKEN", harnessendpoint.AuthAPIKey: "ANTHROPIC_API_KEY"}[style]
		if env[keyVar] != "sk-endpoint" {
			t.Fatalf("%s: %s = %q, want the endpoint key", style, keyVar, env[keyVar])
		}
	}
}

// With case-insensitive keys (Windows), a project's lowercase spelling of an
// endpoint key keeps its value through the account step.
func TestRuntimeEnvKeepsCaseVariantProjectEndpointKey(t *testing.T) {
	previous := envKeysCaseInsensitive
	envKeysCaseInsensitive = true
	t.Cleanup(func() { envKeysCaseInsensitive = previous })

	dir := t.TempDir()
	if _, err := harnessendpoint.ForDataDir(dir).Save("claude-code", harnessendpoint.Input{BaseURL: "https://gateway.example.com", APIKey: "sk-endpoint"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		dataDir:       dir,
		executable:    func() (string, error) { return filepath.Join(dir, "ao"), nil },
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		agentAccounts: savedAPIKeyAccount{},
	}
	const projectURL = "https://gateway.example.com/v2"
	env, err := manager.runtimeEnvChecked("session", "project", "", map[string]string{"anthropic_base_url": projectURL}, "", "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	var values []string
	for key, value := range env {
		if strings.EqualFold(key, "ANTHROPIC_BASE_URL") {
			values = append(values, value)
		}
	}
	if len(values) != 1 || values[0] != projectURL {
		t.Fatalf("base URL entries = %q, want only the project value", values)
	}
	if env["ANTHROPIC_BASE_URL"] != projectURL {
		t.Fatal("the project value must be restored under the canonical environment key")
	}
}

// setupTokenAccount is a session bound to a real Claude setup-token account:
// its launch env comes from the production adapter.
type setupTokenAccount struct {
	ports.AgentAccountLaunchEnv
	home string
}

func (a setupTokenAccount) SessionLaunchEnv(context.Context, domain.SessionID) (map[string]string, error) {
	return (&agentaccount.Claude{}).LaunchEnv(a.home), nil
}

func (a setupTokenAccount) SessionLaunchEnvWith(ctx context.Context, id domain.SessionID, _ string) (map[string]string, error) {
	return a.SessionLaunchEnv(ctx, id)
}

// Claude Code prefers CLAUDE_CODE_OAUTH_TOKEN over both API-key variables, so
// a setup-token account's subscription token must be cleared, not merely
// outranked, when the harness uses a custom endpoint.
func TestRuntimeEnvClearsSetupTokenForCustomEndpoint(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	if err := (&agentaccount.Claude{}).SaveSetupToken(home, "sk-ant-oat01-setuptokenvalue"); err != nil {
		t.Fatal(err)
	}
	if got := (&agentaccount.Claude{}).LaunchEnv(home)["CLAUDE_CODE_OAUTH_TOKEN"]; got == "" {
		t.Fatal("precondition: the setup-token account must supply CLAUDE_CODE_OAUTH_TOKEN")
	}
	if _, err := harnessendpoint.ForDataDir(dir).Save("claude-code", harnessendpoint.Input{BaseURL: "https://gateway.example.com", APIKey: "sk-endpoint"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		dataDir:       dir,
		executable:    func() (string, error) { return filepath.Join(dir, "ao"), nil },
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		agentAccounts: setupTokenAccount{home: home},
	}
	env, err := manager.runtimeEnvChecked("session", "project", "", nil, "", "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if env["ANTHROPIC_BASE_URL"] != "https://gateway.example.com" || env["ANTHROPIC_AUTH_TOKEN"] != "sk-endpoint" {
		t.Fatalf("custom endpoint not applied: %v", env)
	}
	if value, ok := env["CLAUDE_CODE_OAUTH_TOKEN"]; !ok || value != "" {
		t.Fatalf("CLAUDE_CODE_OAUTH_TOKEN = %q (set=%v), want the setup token cleared", value, ok)
	}
}

// A project that points Claude at its own gateway must not receive the saved
// endpoint's key there. A URL alone stops the launch; a URL with the project's
// own key launches with only that key; a URL on the saved origin keeps the
// saved key.
func TestRuntimeEnvKeepsEndpointKeyPairedWithItsURL(t *testing.T) {
	dir := t.TempDir()
	if _, err := harnessendpoint.ForDataDir(dir).Save("claude-code", harnessendpoint.Input{BaseURL: "https://gateway.example.com", APIKey: "sk-endpoint"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		dataDir:    dir,
		executable: func() (string, error) { return filepath.Join(dir, "ao"), nil },
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	launch := func(projectEnv map[string]string) map[string]string {
		t.Helper()
		env, err := manager.runtimeEnvChecked("session", "project", "", projectEnv, "", "claude-code")
		if err != nil {
			t.Fatal(err)
		}
		return env
	}

	urlOnly, err := manager.runtimeEnvChecked("session", "project", "", map[string]string{"ANTHROPIC_BASE_URL": "https://other.example.net"}, "", "claude-code")
	if !errors.Is(err, harnessendpoint.ErrIncompleteOverride) || urlOnly != nil {
		t.Fatalf("a project gateway without its own key must stop the launch: env=%t err=%v", urlOnly != nil, err)
	}
	if _, _, err := manager.launchRuntimeEnvForHarness("session", "project", "", map[string]string{"ANTHROPIC_BASE_URL": "https://other.example.net"}, domain.HarnessClaudeCode); !errors.Is(err, harnessendpoint.ErrIncompleteOverride) {
		t.Fatalf("terminal launch must stop on an incomplete override: %v", err)
	}

	withKey := launch(map[string]string{"ANTHROPIC_BASE_URL": "https://other.example.net", "ANTHROPIC_API_KEY": "sk-project"})
	if withKey["ANTHROPIC_BASE_URL"] != "https://other.example.net" || withKey["ANTHROPIC_API_KEY"] != "sk-project" {
		t.Fatalf("project gateway and key not applied: url=%q", withKey["ANTHROPIC_BASE_URL"])
	}
	if value, ok := withKey["ANTHROPIC_AUTH_TOKEN"]; !ok || value != "" {
		t.Fatalf("ANTHROPIC_AUTH_TOKEN must be blank beside the project's key (set=%v)", ok)
	}

	sameOrigin := launch(map[string]string{"ANTHROPIC_BASE_URL": "https://gateway.example.com/v2"})
	if sameOrigin["ANTHROPIC_BASE_URL"] != "https://gateway.example.com/v2" || sameOrigin["ANTHROPIC_AUTH_TOKEN"] != "sk-endpoint" {
		t.Fatalf("same-origin override must keep the saved key: %v %v", sameOrigin["ANTHROPIC_BASE_URL"], sameOrigin["ANTHROPIC_AUTH_TOKEN"] == "sk-endpoint")
	}
}

// A complete project gateway override (its own URL and key on another origin)
// survives a bound account: the account's base URL and key must not replace
// the gateway the project configured.
func TestRuntimeEnvKeepsCompleteProjectGatewayOverBoundAccount(t *testing.T) {
	dir := t.TempDir()
	if _, err := harnessendpoint.ForDataDir(dir).Save("claude-code", harnessendpoint.Input{BaseURL: "https://gateway.example.com", APIKey: "sk-endpoint"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		dataDir:       dir,
		executable:    func() (string, error) { return filepath.Join(dir, "ao"), nil },
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		agentAccounts: savedAPIKeyAccount{},
	}
	env, err := manager.runtimeEnvChecked("session", "project", "", map[string]string{
		"ANTHROPIC_BASE_URL":   "https://other.example.net",
		"ANTHROPIC_AUTH_TOKEN": "sk-project",
	}, "", "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if env["ANTHROPIC_BASE_URL"] != "https://other.example.net" || env["ANTHROPIC_AUTH_TOKEN"] != "sk-project" {
		t.Fatalf("bound account replaced the project's gateway: url=%q token-is-project=%v", env["ANTHROPIC_BASE_URL"], env["ANTHROPIC_AUTH_TOKEN"] == "sk-project")
	}
	if value, ok := env["ANTHROPIC_API_KEY"]; !ok || value != "" {
		t.Fatalf("the account's API key rode along to the project's gateway (set=%v, account=%v)", ok, value == "sk-account")
	}
	for key, value := range env {
		if value == "sk-endpoint" {
			t.Fatalf("saved endpoint key reached the project's gateway via %s", key)
		}
	}
}

// A Codex session gets the endpoint in AO-owned variables, which the Codex
// adapter turns into launch overrides, and loses AO's saved OpenAI key.
func TestRuntimeEnvAppliesCodexEndpoint(t *testing.T) {
	dir := t.TempDir()
	if err := providerkeys.Save(t.Context(), dir, "OPENAI_API_KEY", "ao-saved"); err != nil {
		t.Fatal(err)
	}
	if _, err := harnessendpoint.ForDataDir(dir).Save("codex", harnessendpoint.Input{BaseURL: "https://gateway.example.com/v1", APIKey: "sk-endpoint"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		dataDir:    dir,
		executable: func() (string, error) { return filepath.Join(dir, "ao"), nil },
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	env, err := manager.runtimeEnvChecked("session", "project", "", nil, "", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if env[harnessendpoint.CodexBaseURLEnv] != "https://gateway.example.com/v1" || env[harnessendpoint.CodexAPIKeyEnv] != "sk-endpoint" {
		t.Fatalf("codex endpoint not applied: %v", env)
	}
	if value, ok := env["OPENAI_API_KEY"]; !ok || value != "" {
		t.Fatalf("OPENAI_API_KEY = %q (set=%v), want blanked beside the endpoint", value, ok)
	}
	if _, ok := env["ANTHROPIC_BASE_URL"]; ok {
		t.Fatalf("claude endpoint variables leaked into codex: %v", env)
	}
}

func TestRuntimeEnvCanonicalizesCodexEndpointOverridesOnWindows(t *testing.T) {
	previous := envKeysCaseInsensitive
	envKeysCaseInsensitive = true
	t.Cleanup(func() { envKeysCaseInsensitive = previous })

	dir := t.TempDir()
	if _, err := harnessendpoint.ForDataDir(dir).Save("codex", harnessendpoint.Input{BaseURL: "https://stored.example.com", APIKey: "sk-stored"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		dataDir:    dir,
		executable: func() (string, error) { return filepath.Join(dir, "ao"), nil },
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	project := map[string]string{
		strings.ToLower(harnessendpoint.CodexBaseURLEnv): "https://project.example.com",
		strings.ToLower(harnessendpoint.CodexAPIKeyEnv):  "sk-project",
		strings.ToLower(harnessendpoint.CodexModelEnv):   "project-model",
		"openai_api_key":     "sk-native",
		"codex_api_key":      "sk-native",
		"codex_access_token": "native-token",
	}
	env, err := manager.runtimeEnvForHarness("session", "project", "", project, domain.HarnessCodex)
	if err != nil {
		t.Fatal(err)
	}
	if env[harnessendpoint.CodexBaseURLEnv] != "https://project.example.com" || env[harnessendpoint.CodexAPIKeyEnv] != "sk-project" {
		t.Fatal("Codex must receive the project endpoint under canonical keys")
	}
	if env[harnessendpoint.CodexModelEnv] != "project-model" {
		t.Fatal("Codex must receive the project model when the saved endpoint has no model")
	}
	spec, _ := harnessendpoint.Lookup("codex")
	for _, key := range spec.ClearEnv {
		if value, found := env[key]; !found || value != "" {
			t.Errorf("%s must be blank with an active custom endpoint", key)
		}
	}
	for key := range project {
		if _, exists := env[key]; exists {
			t.Fatalf("case-variant endpoint key %q is still present", key)
		}
	}
}

func TestRuntimeEnvStopsLaunchOnBrokenEndpointBeforeResolvingAccount(t *testing.T) {
	dir := t.TempDir()
	accounts := &preflightEnvAccounts{}
	manager := &Manager{
		dataDir:       dir,
		executable:    func() (string, error) { return filepath.Join(dir, "ao"), nil },
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		agentAccounts: accounts,
	}
	path := writeBrokenCodexEndpoint(t, dir)
	if env, _, err := manager.launchRuntimeEnvForHarness("session", "project", "", nil, domain.HarnessCodex); err == nil || env != nil {
		t.Fatalf("broken endpoint must stop terminal launch, got a usable env=%t, err=%v", env != nil, err)
	}
	if env, err := manager.chatPreflightEnv(ports.SpawnConfig{Harness: domain.HarnessCodex}, nil); err == nil || env != nil {
		t.Fatalf("broken endpoint must stop Chat admission, got a usable env=%t, err=%v", env != nil, err)
	}
	if len(accounts.ids) != 0 {
		t.Fatalf("account lookup ran for %q after the endpoint read failed", accounts.ids)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if env, _, err := manager.launchRuntimeEnvForHarness("session", "project", "", nil, domain.HarnessCodex); err != nil || env == nil {
		t.Fatalf("missing endpoint must allow native launch, got env=%t, err=%v", env != nil, err)
	}
}

func TestDefaultChatSpawnRejectsBrokenEndpointBeforeCreatingSession(t *testing.T) {
	launcher := &recordingLauncher{}
	manager, store, runtime := newChatManager(launcher)
	manager.defaults = fixedSessionModeDefaults(domain.SessionModeChat)
	manager.dataDir = t.TempDir()
	writeBrokenCodexEndpoint(t, manager.dataDir)

	_, _, _, err := manager.Spawn(t.Context(), ports.SpawnConfig{
		ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: domain.HarnessCodex,
	})
	if err == nil || !strings.Contains(err.Error(), "load custom endpoint") {
		t.Fatalf("expected the endpoint error from Spawn, got %v", err)
	}
	if len(store.sessions) != 0 || len(launcher.preflighted) != 0 || len(launcher.started) != 0 || runtime.created != 0 {
		t.Fatal("broken endpoint should stop before Chat admission, state creation, and TUI fallback")
	}
}

func writeBrokenCodexEndpoint(t *testing.T, dataDir string) string {
	t.Helper()
	path := filepath.Join(dataDir, "harness-endpoints", "codex", "endpoint.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A project that sets a provider switch or a competing credential cannot use
// it while the custom endpoint is active: the endpoint's cleared variables are
// applied after the project's env and stay blank.
func TestRuntimeEnvKeepsClearedVariablesBlankOverProjectEnv(t *testing.T) {
	dir := t.TempDir()
	if _, err := harnessendpoint.ForDataDir(dir).Save("claude-code", harnessendpoint.Input{BaseURL: "https://gateway.example.com", APIKey: "sk-endpoint"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		dataDir:    dir,
		executable: func() (string, error) { return filepath.Join(dir, "ao"), nil },
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	env, err := manager.runtimeEnvChecked("session", "project", "", map[string]string{
		"CLAUDE_CODE_USE_BEDROCK": "1",
		"CLAUDE_CODE_OAUTH_TOKEN": "sk-ant-oat-project",
	}, "", "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_OAUTH_TOKEN"} {
		if value, ok := env[key]; !ok || value != "" {
			t.Fatalf("%s = %q (set=%v), want blank while the custom endpoint is active", key, value, ok)
		}
	}
	if env["ANTHROPIC_AUTH_TOKEN"] != "sk-endpoint" {
		t.Fatalf("endpoint key = %q, want the saved key", env["ANTHROPIC_AUTH_TOKEN"])
	}
}

// With both spellings of the base URL set to different values on Windows, the
// launch stops instead of validating one URL and running with the other.
func TestRuntimeEnvStopsOnConflictingCaseVariantsOfTheEndpointURL(t *testing.T) {
	previous := envKeysCaseInsensitive
	envKeysCaseInsensitive = true
	t.Cleanup(func() { envKeysCaseInsensitive = previous })
	dir := t.TempDir()
	if _, err := harnessendpoint.ForDataDir(dir).Save("claude-code", harnessendpoint.Input{BaseURL: "https://gateway.example.com", APIKey: "sk-endpoint"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		dataDir:    dir,
		executable: func() (string, error) { return filepath.Join(dir, "ao"), nil },
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	_, err := manager.runtimeEnvChecked("session", "project", "", map[string]string{
		"ANTHROPIC_BASE_URL": "https://gateway.example.com",
		"anthropic_base_url": "https://other.example.net",
	}, "", "claude-code")
	if !errors.Is(err, harnessendpoint.ErrIncompleteOverride) {
		t.Fatalf("err = %v, want the launch stopped on conflicting spellings", err)
	}
}
