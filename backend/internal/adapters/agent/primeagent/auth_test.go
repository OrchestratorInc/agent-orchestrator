package primeagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestPrimeProfileAuthStatusAuthorizedFromProviderEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	status, ok, err := primeProfileAuthStatus(context.Background(), primeConfigDir())
	if err != nil {
		t.Fatal(err)
	}
	if !ok || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = (%q, %v), want (%q, true)", status, ok, ports.AgentAuthStatusAuthorized)
	}
}

func TestPrimeProfileAuthStatusAuthorizedFromAuthFile(t *testing.T) {
	clearPrimeCredentialEnv(t)
	dir := t.TempDir()
	t.Setenv(primeAgentCodingAgentDirEnv, dir)
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte(`{"anthropic":{"type":"api_key","key":"test-key"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	status, ok, err := primeProfileAuthStatus(context.Background(), primeConfigDir())
	if err != nil {
		t.Fatal(err)
	}
	if !ok || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = (%q, %v), want (%q, true)", status, ok, ports.AgentAuthStatusAuthorized)
	}
}

func TestPrimeProfileAuthStatusUnknownForEmptyAuthFile(t *testing.T) {
	clearPrimeCredentialEnv(t)
	dir := t.TempDir()
	t.Setenv(primeAgentCodingAgentDirEnv, dir)
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	status, ok, err := primeProfileAuthStatus(context.Background(), primeConfigDir())
	if err != nil {
		t.Fatal(err)
	}
	if ok || status != ports.AgentAuthStatusUnknown {
		t.Fatalf("status = (%q, %v), want (%q, false)", status, ok, ports.AgentAuthStatusUnknown)
	}
}

func TestPrimeProfileAuthStatusAuthorizedFromModelsFileCredentials(t *testing.T) {
	clearPrimeCredentialEnv(t)
	dir := t.TempDir()
	t.Setenv(primeAgentCodingAgentDirEnv, dir)
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(`{"providers":{"custom":{"apiKey":"looks-like-a-key"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, evidence := ports.WithAuthEvidence(context.Background())
	status, ok, err := primeProfileAuthStatus(ctx, primeConfigDir())
	if err != nil {
		t.Fatal(err)
	}
	if !ok || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = (%q, %v), want (%q, true)", status, ok, ports.AgentAuthStatusAuthorized)
	}
	// The providers sit under "providers"; that key is not a provider.
	if got := evidence.Identity(); got.Provider != "custom" || got.Method != domain.AgentAccountMethodAPIKey {
		t.Fatalf("evidence = %+v, want the custom provider's API key", got)
	}
}

func TestPrimeCredentialJSONIgnoresMCPOnlyCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"mcp:notion":{"accessToken":"mcp-token"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	status, ok, _, err := primeCredentialJSONStatus(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if ok || status != ports.AgentAuthStatusUnknown {
		t.Fatalf("status = (%q, %v), want (%q, false)", status, ok, ports.AgentAuthStatusUnknown)
	}
}

func TestPrimeModelsJSONIgnoresMCPOnlyCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	if err := os.WriteFile(path, []byte(`{"mcp:notion":{"accessToken":"mcp-token"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	status, ok, _, err := primeCredentialJSONStatus(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if ok || status != ports.AgentAuthStatusUnknown {
		t.Fatalf("status = (%q, %v), want (%q, false)", status, ok, ports.AgentAuthStatusUnknown)
	}
}

func TestPrimeCredentialJSONAuthorizedFromOAuth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"openai-codex":{"type":"oauth","access":"oauth-token"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	status, ok, _, err := primeCredentialJSONStatus(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = (%q, %v), want (%q, true)", status, ok, ports.AgentAuthStatusAuthorized)
	}
}

func TestAuthStatusForRuntimeReadsTheSessionProfile(t *testing.T) {
	const credential = `{"anthropic":{"type":"api_key","key":"test-key"}}`
	tests := []struct {
		name        string
		fileProfile string
		envKey      bool
		want        ports.AgentAuthStatus
	}{
		{name: "no credentials", want: ports.AgentAuthStatusUnknown},
		{name: "login only in the default profile", fileProfile: "default", want: ports.AgentAuthStatusUnknown},
		{name: "login in the session profile", fileProfile: "session", want: ports.AgentAuthStatusAuthorized},
		{name: "provider key in the environment", envKey: true, want: ports.AgentAuthStatusAuthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearPrimeCredentialEnv(t)
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv(primeAgentCodingAgentDirEnv, "")
			t.Setenv("PI_CODING_AGENT_DIR", "")
			dataDir := t.TempDir()
			profile, err := primeDataDir(dataDir)
			if err != nil {
				t.Fatal(err)
			}
			dirs := map[string]string{"default": filepath.Join(home, ".prime", "agent"), "session": profile}
			if tt.fileProfile != "" {
				dir := dirs[tt.fileProfile]
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(credential), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tt.envKey {
				t.Setenv("ANTHROPIC_API_KEY", "test-key")
			}

			status, _, _, err := (&Plugin{resolvedBinary: "prime-agent"}).AuthStatusForRuntime(context.Background(), dataDir)
			if err != nil {
				t.Fatal(err)
			}
			if status != tt.want {
				t.Fatalf("status = %q, want %q", status, tt.want)
			}
		})
	}
}

func TestAuthStatusForRuntimeRecordsTheSessionProfileAsEvidence(t *testing.T) {
	clearPrimeCredentialEnv(t)
	dataDir := t.TempDir()
	profile, err := primeDataDir(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(profile, "auth.json")
	if err := os.WriteFile(path, []byte(`{"anthropic":{"type":"api_key","key":"test-key"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, evidence := ports.WithAuthEvidence(context.Background())
	if _, _, _, err := (&Plugin{resolvedBinary: "prime-agent"}).AuthStatusForRuntime(ctx, dataDir); err != nil {
		t.Fatal(err)
	}
	if got := evidence.Identity().Source; !strings.Contains(got, path) {
		t.Fatalf("evidence source = %q, want it to name %q", got, path)
	}
}

func TestAuthStatusForRuntimeRequiresDataDir(t *testing.T) {
	if _, _, _, err := (&Plugin{resolvedBinary: "prime-agent"}).AuthStatusForRuntime(context.Background(), " "); err == nil {
		t.Fatal("AuthStatusForRuntime error = nil, want a DataDir error")
	}
}

func clearPrimeCredentialEnv(t *testing.T) {
	t.Helper()
	for _, name := range primeAPIKeyEnvVars {
		t.Setenv(name, "")
	}
	for _, name := range []string{
		"AWS_PROFILE", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_CONTAINER_CREDENTIALS_FULL_URI",
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_WEB_IDENTITY_TOKEN_FILE", "AWS_ROLE_ARN",
		"GOOGLE_APPLICATION_CREDENTIALS", "GOOGLE_CLOUD_PROJECT", "GOOGLE_CLOUD_PROJECT_ID",
		"GCLOUD_PROJECT", "GOOGLE_CLOUD_LOCATION", "XDG_CONFIG_HOME", "APPDATA",
	} {
		t.Setenv(name, "")
	}
}
