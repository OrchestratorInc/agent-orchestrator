package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

// Staging runs its own GitHub App, beside the repository broker it reaches
// production's projects through.
func TestLoadAllowsGitHubAppInStaging(t *testing.T) {
	setGitHubLocalTestEnvironment(t, "staging", false)
	t.Setenv("AO_CLOUD_LOCAL_AUTH", "false")
	t.Setenv("AO_CLOUD_WORKOS_ISSUER", "https://auth.ao-cloud-test.example")
	t.Setenv("AO_CLOUD_WORKOS_CLIENT_ID", "client_staging")
	t.Setenv("AO_CLOUD_WORKOS_API_KEY", "sk_staging")
	t.Setenv("AO_CLOUD_RELEASE", "v1.2.3")
	t.Setenv("AO_CLOUD_SANDBOX_PROVIDER", "freestyle")
	t.Setenv("AO_CLOUD_FREESTYLE_API_KEY", "freestyle-key")
	t.Setenv("AO_CLOUD_FREESTYLE_DEFAULT_SNAPSHOT", "snapshot-1")
	t.Setenv("AO_CLOUD_WORKER_BINARY_PATH", "/usr/local/bin/ao-worker")
	t.Setenv("AO_CLOUD_WORKER_HELPER_BINARY_PATH", "/usr/local/bin/ao")
	t.Setenv("AO_CLOUD_PROVIDER_SECRET_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("AO_CLOUD_REPOSITORY_BROKER_URL", "https://broker.ao-cloud-test.example")
	t.Setenv("AO_CLOUD_ENV_CONTROL_TOKEN", strings.Repeat("c", 32))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.GitHub.Enabled() || cfg.Environment != "staging" {
		t.Fatalf("GitHub App enabled = %v in %q, want enabled in staging", cfg.GitHub.Enabled(), cfg.Environment)
	}
}
