package zcode

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAuthStatusUnknownWithoutConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZCODE_PERSONAL_PROVIDER_CONFIG_FILE", "")
	t.Setenv("ZCODE_DATA_BASE_DIR", "")

	status, err := (&Plugin{}).AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status != ports.AgentAuthStatusUnknown {
		t.Fatalf("status = %q, want unknown", status)
	}
}

func TestAuthStatusAuthorizedWithProviderAccess(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZCODE_PERSONAL_PROVIDER_CONFIG_FILE", "")
	t.Setenv("ZCODE_DATA_BASE_DIR", "")
	path := filepath.Join(home, ".zcode", "v2", "provider_config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	config := `{"schemaVersion":1,"config":{"providerConfigRules":{"providerRules":[{"providerId":"zai","config":{"access":{"type":"api-key","apiKey":"redacted-not-read"}}}]}}}`
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	status, err := (&Plugin{}).AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = %q, want authorized", status)
	}
}

func TestAuthStatusUnknownWithoutAccessEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZCODE_PERSONAL_PROVIDER_CONFIG_FILE", "")
	t.Setenv("ZCODE_DATA_BASE_DIR", "")
	path := filepath.Join(home, ".zcode", "v2", "provider_config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"config":{"providerConfigRules":{"providerRules":[{"providerId":"zai","config":{}}}]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	status, err := (&Plugin{}).AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status != ports.AgentAuthStatusUnknown {
		t.Fatalf("status = %q, want unknown", status)
	}
}

func TestAuthStatusUnknownOnUnparsableConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZCODE_PERSONAL_PROVIDER_CONFIG_FILE", "")
	t.Setenv("ZCODE_DATA_BASE_DIR", "")
	path := filepath.Join(home, ".zcode", "v2", "provider_config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	status, err := (&Plugin{}).AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status != ports.AgentAuthStatusUnknown {
		t.Fatalf("status = %q, want unknown", status)
	}
}
