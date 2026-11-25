package harnessendpoint

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

func TestSaveLoadEnvBearer(t *testing.T) {
	store := NewStore(t.TempDir())
	status, err := store.Save("claude-code", Input{BaseURL: " https://gateway.example.com/ ", APIKey: "sk-test", Model: "glm-4.6"}, testNow)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !status.Configured || !status.HasAPIKey || status.BaseURL != "https://gateway.example.com" || status.AuthStyle != AuthBearer || status.Model != "glm-4.6" {
		t.Fatalf("status = %+v", status)
	}
	env, err := store.Env("claude-code")
	if err != nil {
		t.Fatalf("Env: %v", err)
	}
	want := map[string]string{
		"ANTHROPIC_BASE_URL":      "https://gateway.example.com",
		"ANTHROPIC_AUTH_TOKEN":    "sk-test",
		"ANTHROPIC_API_KEY":       "",
		"ANTHROPIC_MODEL":         "glm-4.6",
		"CLAUDE_CODE_OAUTH_TOKEN": "",
		"CLAUDE_CODE_USE_BEDROCK": "",
		"CLAUDE_CODE_USE_VERTEX":  "",
		"CLAUDE_CODE_USE_FOUNDRY": "",
	}
	if len(env) != len(want) {
		t.Fatalf("env = %v, want %v", env, want)
	}
	for key, value := range want {
		if got, ok := env[key]; !ok || got != value {
			t.Fatalf("env[%s] = %q (present=%v), want %q", key, got, ok, value)
		}
	}
}

func TestEnvAPIKeyStyleBlanksBearer(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, err := store.Save("claude-code", Input{BaseURL: "https://api.example.com", AuthStyle: AuthAPIKey, APIKey: "sk-x"}, testNow); err != nil {
		t.Fatalf("Save: %v", err)
	}
	env, err := store.Env("claude-code")
	if err != nil {
		t.Fatalf("Env: %v", err)
	}
	if env["ANTHROPIC_API_KEY"] != "sk-x" || env["ANTHROPIC_AUTH_TOKEN"] != "" {
		t.Fatalf("env = %v", env)
	}
	if _, ok := env["ANTHROPIC_MODEL"]; ok {
		t.Fatalf("model must not be set when not configured: %v", env)
	}
}

func TestEnvUnconfiguredAndUnsupported(t *testing.T) {
	store := NewStore(t.TempDir())
	for _, agent := range []string{"claude-code", "not-a-harness"} {
		env, err := store.Env(agent)
		if err != nil || env != nil {
			t.Fatalf("Env(%s) = %v, %v", agent, env, err)
		}
	}
	var nilStore *Store
	if env, err := nilStore.Env("claude-code"); err != nil || env != nil {
		t.Fatalf("nil store Env = %v, %v", env, err)
	}
}

func TestForDataDir(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := ForDataDir(dataDir).Save("claude-code", Input{BaseURL: "https://a.example.com", APIKey: "k"}, testNow); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "harness-endpoints", "claude-code", "endpoint.json")); err != nil {
		t.Fatalf("endpoint not stored under the data dir: %v", err)
	}
	if configured, err := ForDataDir("").Configured("claude-code"); err != nil || configured {
		t.Fatalf("empty data dir must disable the store: %v, %v", configured, err)
	}
}

func TestSaveKeepsStoredKeyWhenOmitted(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, err := store.Save("claude-code", Input{BaseURL: "https://a.example.com", APIKey: "sk-keep"}, testNow); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := store.Save("claude-code", Input{BaseURL: "https://b.example.com"}, testNow); err != nil {
		t.Fatalf("Save without key: %v", err)
	}
	cfg, found, err := store.Load("claude-code")
	if err != nil || !found || cfg.APIKey != "sk-keep" || cfg.BaseURL != "https://b.example.com" {
		t.Fatalf("Load = %+v, %v, %v", cfg, found, err)
	}
}

func TestSaveWaitsForWriterInAnotherStore(t *testing.T) {
	root := t.TempDir()
	path, err := NewStore(root).path("claude-code")
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	// Each request builds its own Store; a writer holding the file's lock in
	// one must block a key-keeping save made through another.
	unlock := lockPath(path)
	done := make(chan error, 1)
	go func() {
		_, err := NewStore(root).Save("claude-code", Input{BaseURL: "https://a.example.com", APIKey: "sk-new"}, testNow)
		done <- err
	}()
	select {
	case err := <-done:
		unlock()
		t.Fatalf("Save finished while another store held the lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	unlock()
	if err := <-done; err != nil {
		t.Fatalf("Save after unlock: %v", err)
	}
}

func TestSaveValidation(t *testing.T) {
	cases := map[string]Input{
		"missing key":      {BaseURL: "https://a.example.com"},
		"missing url":      {APIKey: "k"},
		"relative url":     {BaseURL: "gateway.example.com", APIKey: "k"},
		"bad scheme":       {BaseURL: "ftp://a.example.com", APIKey: "k"},
		"remote http":      {BaseURL: "http://gateway.example.com", APIKey: "k"},
		"userinfo in url":  {BaseURL: "https://user:pw@a.example.com", APIKey: "k"},
		"newline in key":   {BaseURL: "https://a.example.com", APIKey: "k\nX=1"},
		"unknown style":    {BaseURL: "https://a.example.com", APIKey: "k", AuthStyle: "basic"},
		"space in model":   {BaseURL: "https://a.example.com", APIKey: "k", Model: "a b"},
		"newline in model": {BaseURL: "https://a.example.com", APIKey: "k", Model: "a\nb"},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NewStore(t.TempDir()).Save("claude-code", in, testNow)
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("err = %v, want ValidationError", err)
			}
		})
	}
}

func TestSaveAllowsLoopbackHTTP(t *testing.T) {
	for _, baseURL := range []string{"http://localhost:4000", "http://127.0.0.1:8080/v1", "http://[::1]:9000"} {
		store := NewStore(t.TempDir())
		if _, err := store.Save("claude-code", Input{BaseURL: baseURL, APIKey: "k"}, testNow); err != nil {
			t.Fatalf("Save(%s): %v", baseURL, err)
		}
		if env, err := store.Env("claude-code"); err != nil || env["ANTHROPIC_BASE_URL"] == "" {
			t.Fatalf("Env(%s) = %v, %v", baseURL, env, err)
		}
	}
}

func TestEnvRefusesStoredCleartextRemoteURL(t *testing.T) {
	store := NewStore(t.TempDir())
	path, err := store.path("claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"baseUrl":"http://gateway.example.com","authStyle":"bearer","apiKey":"k"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if env, err := store.Env("claude-code"); err == nil || env != nil {
		t.Fatalf("Env = %v, %v; want the cleartext URL refused", env, err)
	}
}

func TestEnvRejectsIncompleteSavedEndpoint(t *testing.T) {
	store := NewStore(t.TempDir())
	path, err := store.path("codex")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"baseUrl":"https://gateway.example.com"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if env, err := store.Env("codex"); err == nil || env != nil {
		t.Fatalf("incomplete endpoint must fail instead of allowing a native launch, got env=%t, err=%v", env != nil, err)
	}
}

func TestSaveReplacesCorruptConfigOnlyWithNewKey(t *testing.T) {
	store := NewStore(t.TempDir())
	path, err := store.path("claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save("claude-code", Input{BaseURL: "https://a.example.com"}, testNow); err == nil {
		t.Fatal("an update keeping the key must fail while the stored config is unreadable")
	}
	if _, err := store.Save("claude-code", Input{BaseURL: "https://a.example.com", APIKey: "sk-new"}, testNow); err != nil {
		t.Fatalf("complete replacement: %v", err)
	}
	if cfg, found, err := store.Load("claude-code"); err != nil || !found || cfg.APIKey != "sk-new" {
		t.Fatalf("Load = %+v, %v, %v", cfg, found, err)
	}
}

func TestSaveUnsupportedHarness(t *testing.T) {
	_, err := NewStore(t.TempDir()).Save("aider", Input{BaseURL: "https://a.example.com", APIKey: "k"}, testNow)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestSaveWritesOwnerOnlyFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	root := t.TempDir()
	if _, err := NewStore(root).Save("claude-code", Input{BaseURL: "https://a.example.com", APIKey: "k"}, testNow); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(filepath.Join(root, "claude-code", "endpoint.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("perm = %o, want 600", perm)
	}
}

func TestDeleteAndStatus(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, err := store.Save("claude-code", Input{BaseURL: "https://a.example.com", APIKey: "k"}, testNow); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Delete("claude-code"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := store.Delete("claude-code"); err != nil {
		t.Fatalf("second Delete: %v", err)
	}
	status, ok, err := store.Status("claude-code")
	if err != nil || !ok || status.Configured || status.HasAPIKey {
		t.Fatalf("Status = %+v, %v, %v", status, ok, err)
	}
	if len(status.AuthStyles) != 2 || status.DefaultAuthStyle != AuthBearer || !status.SupportsModel {
		t.Fatalf("spec status = %+v", status)
	}
	if _, ok, _ := store.Status("aider"); ok {
		t.Fatal("codex must report unsupported until it has a spec")
	}
}

// Codex takes its endpoint from launch overrides, so the store hands the
// adapter AO-owned variables and blanks Codex's own sign-in credentials.
func TestCodexEnvUsesAOVariablesAndClearsCodexCredentials(t *testing.T) {
	store := NewStore(t.TempDir())
	status, err := store.Save("codex", Input{BaseURL: "https://gateway.example.com/v1", APIKey: "sk-codex", Model: "gpt-oss-120b"}, testNow)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if len(status.AuthStyles) != 1 || status.AuthStyles[0] != AuthBearer || !status.SupportsModel {
		t.Fatalf("codex status = %+v, want bearer only with a model", status)
	}
	env, err := store.Env("codex")
	if err != nil {
		t.Fatalf("Env: %v", err)
	}
	want := map[string]string{
		CodexBaseURLEnv:      "https://gateway.example.com/v1",
		CodexAPIKeyEnv:       "sk-codex",
		CodexModelEnv:        "gpt-oss-120b",
		"OPENAI_API_KEY":     "",
		"CODEX_API_KEY":      "",
		"CODEX_ACCESS_TOKEN": "",
	}
	if len(env) != len(want) {
		t.Fatalf("env = %v, want %v", env, want)
	}
	for key, value := range want {
		if got, ok := env[key]; !ok || got != value {
			t.Fatalf("env[%s] = %q (present=%v), want %q", key, got, ok, value)
		}
	}
	if _, err := store.Save("codex", Input{BaseURL: "https://gateway.example.com", APIKey: "k", AuthStyle: AuthAPIKey}, testNow); err == nil {
		t.Fatal("codex accepts only the bearer style")
	}
}

func TestProjectEnvKeepsKeyWithItsOrigin(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, err := store.Save("claude-code", Input{BaseURL: "https://gateway.example.com/v1", APIKey: "sk-endpoint"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		project map[string]string
		insens  bool
		applied bool
		wantErr bool
	}{
		"no project env":            {project: nil, applied: true},
		"unrelated project keys":    {project: map[string]string{"ANTHROPIC_MODEL": "m"}, applied: true},
		"same origin other path":    {project: map[string]string{"ANTHROPIC_BASE_URL": "https://GATEWAY.example.com:443/v2"}, applied: true},
		"other origin, url only":    {project: map[string]string{"ANTHROPIC_BASE_URL": "https://other.example.net"}, wantErr: true},
		"other port, url only":      {project: map[string]string{"ANTHROPIC_BASE_URL": "https://gateway.example.com:8443"}, wantErr: true},
		"blanked url, no key":       {project: map[string]string{"ANTHROPIC_BASE_URL": ""}, wantErr: true},
		"other origin, project key": {project: map[string]string{"ANTHROPIC_BASE_URL": "https://other.example.net", "ANTHROPIC_API_KEY": "sk-project"}},
		"other origin, windows key": {project: map[string]string{"anthropic_base_url": "https://other.example.net", "Anthropic_Auth_Token": "sk-project"}, insens: true},
	} {
		t.Run(name, func(t *testing.T) {
			env, err := store.ProjectEnv("claude-code", tc.project, tc.insens)
			if tc.wantErr != errors.Is(err, ErrIncompleteOverride) {
				t.Fatalf("err = %v, want incomplete override %v", err, tc.wantErr)
			}
			if got := env["ANTHROPIC_AUTH_TOKEN"] == "sk-endpoint"; got != tc.applied {
				t.Fatalf("saved endpoint applied = %v, want %v (env %v)", got, tc.applied, env)
			}
		})
	}
	if env, err := store.ProjectEnv("goose", map[string]string{"ANTHROPIC_BASE_URL": "https://x.example"}, false); env != nil || err != nil {
		t.Fatalf("unsupported harness: %v %v", env, err)
	}
}

// A complete override on another origin replaces the saved endpoint with the
// project's gateway and key, blanking the other key variable and competing
// credentials so a bound account's cannot ride along.
func TestProjectEnvReturnsCompleteProjectOverride(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, err := store.Save("claude-code", Input{BaseURL: "https://gateway.example.com", APIKey: "sk-endpoint", Model: "saved-model"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	env, err := store.ProjectEnv("claude-code", map[string]string{
		"ANTHROPIC_BASE_URL": "https://other.example.net",
		"ANTHROPIC_API_KEY":  "sk-project",
		"ANTHROPIC_MODEL":    "project-model",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"ANTHROPIC_BASE_URL":      "https://other.example.net",
		"ANTHROPIC_API_KEY":       "sk-project",
		"ANTHROPIC_AUTH_TOKEN":    "",
		"ANTHROPIC_MODEL":         "project-model",
		"CLAUDE_CODE_OAUTH_TOKEN": "",
		"CLAUDE_CODE_USE_BEDROCK": "",
		"CLAUDE_CODE_USE_VERTEX":  "",
		"CLAUDE_CODE_USE_FOUNDRY": "",
	}
	if len(env) != len(want) {
		t.Fatalf("env has %d entries, want %d: %v", len(env), len(want), keysOf(env))
	}
	for key, value := range want {
		if got, ok := env[key]; !ok || got != value {
			t.Fatalf("env[%s] = %q (set=%v), want %q", key, got, ok, value)
		}
	}
}

func keysOf(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	return keys
}
