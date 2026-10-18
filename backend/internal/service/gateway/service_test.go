package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func newTestService(t *testing.T, workDir string) *Service {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "claude-home")
	return New(func() (string, error) { return dir, nil }, func(_ context.Context, _ string) (string, error) {
		return workDir, nil
	})
}

func str(v string) *string { return &v }

func readEnvMap(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	env, _ := doc["env"].(map[string]any)
	if env == nil {
		env = map[string]any{}
	}
	return env
}

func TestSetAppTriStatePersistsToFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "claude-home")
	svc := New(func() (string, error) { return dir, nil }, func(_ context.Context, _ string) (string, error) { return "", nil })
	path := filepath.Join(dir, "settings.json")

	cfg, err := svc.Set(context.Background(), SetInput{Scope: ScopeApp, BaseURL: str(" https://gw.example "), Token: str("tok"), Model: str("glm-5")})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if cfg.App.BaseURL != "https://gw.example" || !cfg.App.TokenSet || cfg.Effective.Source != "app" {
		t.Fatalf("config=%+v", cfg)
	}
	env := readEnvMap(t, path)
	if env["ANTHROPIC_BASE_URL"] != "https://gw.example" || env["ANTHROPIC_AUTH_TOKEN"] != "tok" || env["ANTHROPIC_MODEL"] != "glm-5" {
		t.Fatalf("env=%v", env)
	}

	// Token-only rotation: nil keys stay, the cleared model key disappears.
	if _, err := svc.Set(context.Background(), SetInput{Scope: ScopeApp, Token: str("tok2"), Model: str("")}); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	env = readEnvMap(t, path)
	if env["ANTHROPIC_AUTH_TOKEN"] != "tok2" {
		t.Fatalf("token=%v", env)
	}
	if _, cleared := env["ANTHROPIC_MODEL"]; cleared {
		t.Fatalf("model should be deleted, got %v", env)
	}
	if env["ANTHROPIC_BASE_URL"] != "https://gw.example" {
		t.Fatalf("baseURL must survive rotation, got %v", env)
	}
}

func TestSetProjectOverridesAppInEffective(t *testing.T) {
	workDir := t.TempDir()
	svc := newTestService(t, workDir)
	if _, err := svc.Set(context.Background(), SetInput{Scope: ScopeApp, BaseURL: str("https://app.example")}); err != nil {
		t.Fatalf("set app: %v", err)
	}
	if _, err := svc.Set(context.Background(), SetInput{Scope: ScopeProject, ProjectID: "p1", BaseURL: str("https://proj.example"), Model: str("m")}); err != nil {
		t.Fatalf("set project: %v", err)
	}
	cfg, err := svc.Get(context.Background(), "p1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if cfg.Effective.BaseURL != "https://proj.example" || cfg.Effective.Source != "project" {
		t.Fatalf("effective=%+v", cfg.Effective)
	}
	if cfg.Project == nil || cfg.Project.Model != "m" {
		t.Fatalf("project=%+v", cfg.Project)
	}

	// Clearing the whole project entry falls back to app.
	if _, err := svc.Set(context.Background(), SetInput{Scope: ScopeProject, ProjectID: "p1", BaseURL: str(""), Model: str("")}); err != nil {
		t.Fatalf("clear project: %v", err)
	}
	cfg, err = svc.Get(context.Background(), "p1")
	if err != nil {
		t.Fatalf("get after clear: %v", err)
	}
	if cfg.Effective.Source != "app" || cfg.Effective.BaseURL != "https://app.example" {
		t.Fatalf("effective after clear=%+v", cfg.Effective)
	}
}

func TestProjectLocalSettingsWinInEffective(t *testing.T) {
	workDir := t.TempDir()
	claudeDir := filepath.Join(workDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.local.json"), []byte(`{"env":{"ANTHROPIC_BASE_URL":"https://local.example"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := newTestService(t, workDir)
	cfg, err := svc.Get(context.Background(), "p1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if cfg.Effective.BaseURL != "https://local.example" {
		t.Fatalf("effective=%+v", cfg.Effective)
	}
	// A UI save still targets settings.json, not settings.local.json.
	if _, err := svc.Set(context.Background(), SetInput{Scope: ScopeProject, ProjectID: "p1", BaseURL: str("https://proj.example")}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if env := readEnvMap(t, filepath.Join(claudeDir, "settings.json")); env["ANTHROPIC_BASE_URL"] != "https://proj.example" {
		t.Fatalf("settings.json=%v", env)
	}
}

func TestSetPreservesForeignKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	existing := `{
  "model": "opus",
  "permissions": {"allow": ["Bash"]},
  "env": {"ANTHROPIC_BASE_URL": "https://old.example", "OTHER_KEY": "keep"}
}`
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := New(func() (string, error) { return dir, nil }, func(_ context.Context, _ string) (string, error) { return "", nil })
	if _, err := svc.Set(context.Background(), SetInput{Scope: ScopeApp, BaseURL: str("https://new.example")}); err != nil {
		t.Fatalf("set: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc["model"] != "opus" {
		t.Fatalf("model=%v", doc["model"])
	}
	perms, ok := doc["permissions"].(map[string]any)
	if !ok || len(perms["allow"].([]any)) != 1 {
		t.Fatalf("permissions=%v", doc["permissions"])
	}
	env := doc["env"].(map[string]any)
	if env["OTHER_KEY"] != "keep" || env["ANTHROPIC_BASE_URL"] != "https://new.example" {
		t.Fatalf("env=%v", env)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v, want original 0600", info.Mode().Perm())
	}
}

func TestSetRejectsBadScopeURLAndProject(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	ctx := context.Background()
	if _, err := svc.Set(ctx, SetInput{Scope: "galaxy"}); err == nil {
		t.Fatal("unknown scope accepted")
	}
	if _, err := svc.Set(ctx, SetInput{Scope: ScopeProject}); err == nil {
		t.Fatal("project scope without projectId accepted")
	}
	if _, err := svc.Set(ctx, SetInput{Scope: ScopeApp, BaseURL: str("ftp://x")}); err == nil {
		t.Fatal("non-http URL accepted")
	}
	if _, err := svc.Set(ctx, SetInput{Scope: ScopeApp, BaseURL: str("https://ok")}); err != nil {
		t.Fatalf("valid set rejected: %v", err)
	}
}

func TestGetUnconfiguredScopesAreEmpty(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	cfg, err := svc.Get(context.Background(), "p1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if cfg.App.BaseURL != "" || cfg.App.TokenSet || cfg.Project == nil || cfg.Project.TokenSet {
		t.Fatalf("config=%+v", cfg)
	}
	if cfg.Effective.Source != "" {
		t.Fatalf("effective=%+v", cfg.Effective)
	}
}

func TestProbeValidatesInputs(t *testing.T) {
	svc := newTestService(t, t.TempDir())
	ctx := context.Background()
	if _, err := svc.Probe(ctx, "", "tok"); err == nil {
		t.Fatal("blank base URL accepted")
	}
	if _, err := svc.Probe(ctx, "https://gw.example", ""); err == nil {
		t.Fatal("blank token accepted")
	}
	if _, err := svc.Probe(ctx, "ftp://gw.example", "tok"); err == nil {
		t.Fatal("non-http scheme accepted")
	}
}
