package agentauth

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/providerkeys"
)

func TestQwenProviderLoginSelectsModelAndPreservesSettings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QWEN_HOME", filepath.Join(dir, "qwen"))
	if err := os.MkdirAll(os.Getenv("QWEN_HOME"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(os.Getenv("QWEN_HOME"), "settings.json")
	if err := os.WriteFile(path, []byte(`{"theme":"dark","security":{"auth":{"other":true}},"modelProviders":{"openai":[{"id":"old-model","envKey":"EXISTING_KEY"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	previous := publishProviderKey
	publishProviderKey = func(_ context.Context, name, value string) error {
		if name != "OPENAI_API_KEY" || value != "test-secret" {
			t.Fatal("unexpected device publication")
		}
		return nil
	}
	t.Cleanup(func() { publishProviderKey = previous })
	t.Setenv("OPENAI_API_KEY", "")
	svc := NewWithAgentResolver(foundExecutable("qwen"), nil, nil, dir)
	if _, err := svc.StartLogin(t.Context(), "qwen", "env_OPENAI_API_KEY", "test-secret"); err == nil {
		t.Fatal("accepted Qwen provider without a model")
	}
	result, err := svc.StartLoginWithModel(t.Context(), "qwen", "env_OPENAI_API_KEY", "test-secret", "gpt-4o")
	if err != nil || result.Status != "succeeded" {
		t.Fatalf("Qwen login = %+v, %v", result, err)
	}
	keys, err := providerkeys.Load(dir)
	if err != nil || keys["OPENAI_API_KEY"] != "test-secret" {
		t.Fatalf("AO keys = %v, %v", keys, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" || json.Valid(data) == false {
		t.Fatal("invalid Qwen settings")
	}
	var settings struct {
		Theme    string `json:"theme"`
		Security struct {
			Auth struct {
				SelectedType string `json:"selectedType"`
				Other        bool   `json:"other"`
			} `json:"auth"`
		} `json:"security"`
		Model struct {
			Name string `json:"name"`
		} `json:"model"`
		ModelProviders map[string][]struct {
			ID      string `json:"id"`
			EnvKey  string `json:"envKey"`
			BaseURL string `json:"baseUrl"`
		} `json:"modelProviders"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Theme != "dark" || !settings.Security.Auth.Other || settings.Security.Auth.SelectedType != "openai" || settings.Model.Name != "gpt-4o" ||
		len(settings.ModelProviders["openai"]) != 2 || settings.ModelProviders["openai"][0].ID != "old-model" ||
		settings.ModelProviders["openai"][1].EnvKey != "OPENAI_API_KEY" || settings.ModelProviders["openai"][1].BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("Qwen settings lost provider or user data: %+v", settings)
	}
	if _, err := svc.StartLoginWithModel(t.Context(), "qwen", "env_OPENAI_API_KEY", "test-secret", "gpt-4o"); err != nil {
		t.Fatalf("idempotent Qwen setup: %v", err)
	}
	data, _ = os.ReadFile(path)
	_ = json.Unmarshal(data, &settings)
	if len(settings.ModelProviders["openai"]) != 2 {
		t.Fatal("duplicate Qwen model route")
	}
}

func TestQwenProviderRejectsConflictingRoute(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QWEN_HOME", dir)
	path := filepath.Join(dir, "settings.json")
	before := []byte(`{"modelProviders":{"openai":[{"id":"gpt-4o","envKey":"OTHER_KEY"}]}}`)
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := saveQwenProvider(t.Context(), "OPENAI_API_KEY", "gpt-4o"); err == nil {
		t.Fatal("overwrote conflicting model route")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("changed Qwen settings after conflict")
	}
}
