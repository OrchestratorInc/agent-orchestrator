package usage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

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
		{"", "zai"},
		{"glm-5.2", "zai"},
		{"glm-5.3", "zai"},
		{"gpt-test", "openai"},
		{"GLM-5.2(openai)", "zai"},
	} {
		session := domain.SessionRecord{Harness: domain.HarnessQwen}
		session.Metadata.Model = tc.model
		if got := collector.qwenProviderHint(context.Background(), session); got != tc.want {
			t.Fatalf("model %q: hint = %q, want %q", tc.model, got, tc.want)
		}
	}
}

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
		{`{"security":{"auth":{"selectedType":"openai"}},"model":{"baseUrl":"https://open.bigmodel.cn/api/paas/v4"}}`, "bigmodel"},
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

func TestQwenEndpointProviderNamesAnyCompatibleServer(t *testing.T) {
	for baseURL, want := range map[string]string{
		"https://api.fireworks.ai/inference/v1":     "fireworks_ai",
		"https://api.together.xyz/v1":               "together_ai",
		"https://api.deepinfra.com/v1/openai":       "deepinfra",
		"https://api.cerebras.ai/v1":                "cerebras",
		"https://api.novita.ai/openai":              "novita",
		"https://inference.baseten.co/v1":           "baseten",
		"https://api.studio.nebius.com/v1":          "nebius",
		"https://api.deepseek.com/anthropic":        "deepseek",
		"https://api.minimax.io/anthropic":          "minimax",
		"https://openrouter.ai/api/v1":              "openrouter",
		"https://router.requesty.ai/v1":             "requesty",
		"https://api.cohere.com/compatibility/v1":   "cohere_chat",
		"https://api.friendli.ai/serverless/v1":     "friendliai",
		"https://api.lambda.ai/v1":                  "lambda_ai",
		"https://api.lambdalabs.com/v1":             "lambda_ai",
		"https://ollama.com/v1":                     "ollama",
		"https://api.anthropic.com/v1":              "anthropic",
		"https://api.openai.com/v1":                 "openai",
		"https://API.Mistral.AI./v1":                "mistral",
		"https://generativelanguage.googleapis.com": "gemini",
		"https://us-central1-aiplatform.googleapis.com/v1/projects/p/locations/us-central1/endpoints/openapi": "vertex_ai",
		"https://my-res.openai.azure.com/openai/v1":                                                           "azure",
		"https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1":                                           "bedrock",
		"https://api.x.ai/v1":                                                "xai",
		"https://api.z.ai/api/paas/v4":                                       "zai",
		"https://dashscope-intl.aliyuncs.com/compatible-mode/v1":             "dashscope",
		"https://dashscope.aliyuncs.com/compatible-mode/v1":                  "dashscope-cn",
		"https://coding-intl.dashscope.aliyuncs.com/v1":                      "dashscope-coding-plan",
		"https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1": "dashscope-token-plan",
		"https://maas.qwencloudapi.com/compatible-mode/v1":                   "qwencloud",
		"https://open.bigmodel.cn/api/paas/v4":                               "bigmodel",
		"https://api.minimaxi.com/v1":                                        "minimax-cn",
		"https://api.moonshot.cn/v1":                                         "moonshot-cn",
		"https://api.moonshot.ai/v1":                                         "moonshot",
		"https://api.example.com.cn/v1":                                      "example-cn",
		"https://llm.acme.co.uk/v1":                                          "acme",
		"http://localhost:11434/v1":                                          "",
		"http://127.0.0.1:8000/v1":                                           "",
		"http://vllm.internal:8000/v1":                                       "",
		"http://gpu-box:1234/v1":                                             "",
		"":                                                                   "",
	} {
		if got := qwenEndpointProvider(baseURL); got != want {
			t.Errorf("qwenEndpointProvider(%q) = %q, want %q", baseURL, got, want)
		}
	}
}

func TestQwenRouteFollowsEveryPlaceQwenReadsAnEndpoint(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")
	for _, tc := range []struct {
		settings, model, authType, want string
	}{
		{`{"modelProviders":{"idealab":[{"id":"kimi-k3","baseUrl":"https://api.moonshot.ai/v1"}]}}`, "kimi-k3", "openai", "moonshot"},
		{`{"modelProviders":{"idealab":[{"id":"kimi-k3","baseUrl":"https://api.moonshot.ai/v1"}]}}`, "kimi-k3", "idealab", "moonshot"},
		{`{"security":{"auth":{"selectedType":"openai","baseUrl":"https://api.groq.com/openai/v1"}}}`, "", "", "groq"},
		{`{"security":{"auth":{"selectedType":"anthropic"}},"env":{"ANTHROPIC_BASE_URL":"https://api.minimax.io/anthropic"}}`, "", "", "minimax"},
		{`{"security":{"auth":{"selectedType":"anthropic"}},"env":{"OPENAI_BASE_URL":"https://api.groq.com/openai/v1"}}`, "", "", "anthropic"},
		{`{"model":{"baseUrl":"https://api.deepseek.com/v1"},"modelProviders":{"a":[{"id":"m","baseUrl":"https://api.together.xyz/v1"}],"b":[{"id":"m","baseUrl":"https://api.fireworks.ai/inference/v1"}]}}`, "m", "openai", "deepseek"},
	} {
		var route qwenRoute
		route.merge([]byte(tc.settings))
		if got := route.provider(tc.model, tc.authType); got != tc.want {
			t.Errorf("settings %s model %q auth %q: provider = %q, want %q", tc.settings, tc.model, tc.authType, got, tc.want)
		}
	}
}
