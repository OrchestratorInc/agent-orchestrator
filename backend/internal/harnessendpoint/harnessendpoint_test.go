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
	_, err := NewStore(t.TempDir()).Save("codex", Input{BaseURL: "https://a.example.com", APIKey: "k"}, testNow)
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
	if _, ok, _ := store.Status("codex"); ok {
		t.Fatal("codex must report unsupported until it has a spec")
	}
}
