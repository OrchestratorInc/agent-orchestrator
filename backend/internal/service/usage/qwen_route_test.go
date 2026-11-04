package usage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The shape of a real z.ai Qwen setup: the default model and every configured
// model point at the z.ai coding endpoint, and AO may launch a model the list
// does not name (glm-5.3), which Qwen sends to the default endpoint.
const qwenZaiSettings = `{
  "model": {"name": "GLM-5.2", "baseUrl": "https://api.z.ai/api/coding/paas/v4"},
  "security": {"auth": {"selectedType": "openai"}},
  "modelProviders": {"openai": [
    {"id": "GLM-5.2", "baseUrl": "https://api.z.ai/api/coding/paas/v4"},
    {"id": "gpt-test", "baseUrl": "https://api.openai.com/v1"}
  ]}
}`

func TestQwenProviderHintResolvesSettingsRoute(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("QWEN_HOME", "")
	t.Setenv("OPENAI_BASE_URL", "")
	mustWriteQwenSettings(t, filepath.Join(home, ".qwen", "settings.json"), qwenZaiSettings)
	collector := NewCollector(collectorTestStore(t), SourceRoots{}, nil)

	for _, tc := range []struct{ model, want string }{
		{"", "zai"},        // Qwen's default model
		{"glm-5.2", "zai"}, // a configured model, matched case-insensitively
		{"glm-5.3", "zai"}, // unlisted: Qwen uses the default endpoint
		{"gpt-test", "openai"},
		{"GLM-5.2(openai)", "zai"}, // Qwen's ACP model ID carries the auth type
	} {
		session := domain.SessionRecord{Harness: domain.HarnessQwen}
		session.Metadata.Model = tc.model
		if got := collector.qwenProviderHint(context.Background(), session); got != tc.want {
			t.Fatalf("model %q: hint = %q, want %q", tc.model, got, tc.want)
		}
	}
}

// Break caught: reading only the user's settings. Qwen lets a workspace
// override the route, and the session's own endpoint is the one that billed.
func TestQwenProviderHintPrefersWorkspaceSettings(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("QWEN_HOME", "")
	t.Setenv("OPENAI_BASE_URL", "")
	mustWriteQwenSettings(t, filepath.Join(home, ".qwen", "settings.json"), qwenZaiSettings)
	mustWriteQwenSettings(t, filepath.Join(workspace, ".qwen", "settings.json"),
		`{"model": {"baseUrl": "https://openrouter.ai/api/v1"}}`)
	session := domain.SessionRecord{Harness: domain.HarnessQwen}
	session.Metadata.WorkspacePath = workspace
	session.Metadata.Model = "glm-5.3"
	if got := NewCollector(collectorTestStore(t), SourceRoots{}, nil).qwenProviderHint(context.Background(), session); got != "openrouter" {
		t.Fatalf("hint = %q, want openrouter", got)
	}
}

// An endpoint AO cannot name must leave the route empty: guessing would price a
// self-hosted or proxied model at some public provider's list rate.
func TestQwenRouteProviderLeavesUnknownEndpointsEmpty(t *testing.T) {
	for _, tc := range []struct {
		settings, want string
	}{
		{`{"security":{"auth":{"selectedType":"openai"}},"model":{"baseUrl":"http://localhost:8000/v1"}}`, ""},
		{`{"security":{"auth":{"selectedType":"qwen-oauth"}}}`, ""},
		{`{"security":{"auth":{"selectedType":"openai"}}}`, "openai"},
		{`{"security":{"auth":{"selectedType":"gemini"}}}`, "gemini"},
		{`{"security":{"auth":{"selectedType":"vertex-ai"}}}`, "vertex_ai"},
		{`{"security":{"auth":{"selectedType":"anthropic"}}}`, "anthropic"},
		{`{"security":{"auth":{"selectedType":"anthropic"}},"model":{"baseUrl":"https://api.z.ai/api/anthropic"}}`, "zai"},
		{`{"security":{"auth":{"selectedType":"openai"}},"env":{"OPENAI_BASE_URL":"https://dashscope-intl.aliyuncs.com/compatible-mode/v1"}}`, "dashscope"},
		{`{"security":{"auth":{"selectedType":"openai"}},"model":{"name":"MiniMax-M3"},"modelProviders":{"openai":[{"id":"MiniMax-M3","baseUrl":"https://api.minimax.io/v1"}]}}`, "minimax"},
	} {
		t.Setenv("OPENAI_BASE_URL", "")
		var route qwenRoute
		route.merge([]byte(tc.settings))
		if got := route.provider("", ""); got != tc.want {
			t.Fatalf("settings %s: provider = %q, want %q", tc.settings, got, tc.want)
		}
	}
}

func mustWriteQwenSettings(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Break caught: re-reading Qwen's shared settings on every hook. Qwen rewrites
// the default model whenever any session picks one, so a z.ai session's later
// hook resolved the MiniMax session's route and billed its next reply there.
func TestQwenProviderHintSurvivesAnotherSessionsSelection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("QWEN_HOME", "")
	t.Setenv("OPENAI_BASE_URL", "")
	settingsPath := filepath.Join(home, ".qwen", "settings.json")
	mustWriteQwenSettings(t, settingsPath, `{
  "model": {"name": "GLM-5.2"},
  "security": {"auth": {"selectedType": "openai"}},
  "modelProviders": {"openai": [
    {"id": "GLM-5.2", "baseUrl": "https://api.z.ai/api/coding/paas/v4"},
    {"id": "MiniMax-M3", "baseUrl": "https://api.minimax.io/v1"}
  ]}
}`)
	store := collectorTestStore(t)
	session := collectorTestSession(t, store, domain.HarnessQwen, "qwen-native", false)
	collector := NewCollector(store, SourceRoots{}, nil)
	first, err := collector.stickyQwenProviderHint(context.Background(), session)
	if err != nil || first != "zai" {
		t.Fatalf("first hint = %q, err = %v", first, err)
	}
	if _, err := store.UpsertUsageBinding(context.Background(), domain.UsageBindingRecord{
		SessionID: session.ID, Harness: domain.HarnessQwen, NativeRootID: "qwen-native",
		ProviderHint: first, State: domain.UsageBindingActive, UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	// Another session selects MiniMax, and Qwen writes it as the default.
	mustWriteQwenSettings(t, settingsPath, `{
  "model": {"name": "MiniMax-M3"},
  "security": {"auth": {"selectedType": "openai"}},
  "modelProviders": {"openai": [
    {"id": "GLM-5.2", "baseUrl": "https://api.z.ai/api/coding/paas/v4"},
    {"id": "MiniMax-M3", "baseUrl": "https://api.minimax.io/v1"}
  ]}
}`)
	if got, err := collector.stickyQwenProviderHint(context.Background(), session); err != nil || got != "zai" {
		t.Fatalf("hint after another session's selection = %q, err = %v, want zai", got, err)
	}
}
