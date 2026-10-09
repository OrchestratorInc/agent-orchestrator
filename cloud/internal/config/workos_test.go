package config

import (
	"strings"
	"testing"
)

func TestResolveWorkOS(t *testing.T) {
	cases := []struct {
		name                                string
		issuer, clientID, apiKey, jwksURL   string
		wantIssuer, wantJWKS, wantErrSubstr string
	}{
		{name: "unset"},
		{
			name: "api base URL expands to client issuer", issuer: "https://api.workos.com", clientID: "client_a", apiKey: "sk",
			wantIssuer: "https://api.workos.com/user_management/client_a", wantJWKS: "https://api.workos.com/sso/jwks/client_a",
		},
		{
			name: "custom AuthKit domain", issuer: "https://auth.orchestrator.inc", clientID: "client_a", apiKey: "sk",
			wantIssuer: "https://auth.orchestrator.inc", wantJWKS: "https://auth.orchestrator.inc/oauth2/jwks",
		},
		{name: "partial", issuer: "https://auth.orchestrator.inc", clientID: "client_a", wantErrSubstr: "must be set together"},
		{
			name: "issuer for another client", issuer: "https://api.workos.com/user_management/client_b", clientID: "client_a", apiKey: "sk",
			wantErrSubstr: "must match",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issuer, jwks, err := resolveWorkOS("AO_CLOUD_WORKOS", tc.issuer, tc.clientID, tc.apiKey, tc.jwksURL)
			if tc.wantErrSubstr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrSubstr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErrSubstr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if issuer != tc.wantIssuer || jwks != tc.wantJWKS {
				t.Fatalf("got %q / %q, want %q / %q", issuer, jwks, tc.wantIssuer, tc.wantJWKS)
			}
		})
	}
}

func setWorkOSTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AO_CLOUD_ENV", "development")
	t.Setenv("AO_CLOUD_DATABASE_URL", "postgres://example.invalid/ao")
	t.Setenv("AO_CLOUD_LOCAL_AUTH", "false")
	t.Setenv("AO_CLOUD_SANDBOX_PROVIDER", "docker")
	t.Setenv("AO_CLOUD_PUBLIC_URL", "https://ao-cloud-test.example")
	t.Setenv("AO_CLOUD_WORKER_SIGNING_KEY", strings.Repeat("w", 32))
	t.Setenv("AO_CLOUD_WORKOS_ISSUER", "https://auth.orchestrator.inc")
	t.Setenv("AO_CLOUD_WORKOS_CLIENT_ID", "client_production")
	t.Setenv("AO_CLOUD_WORKOS_API_KEY", "sk_production")
}

func TestLoadAcceptsALegacyWorkOSEnvironment(t *testing.T) {
	setWorkOSTestEnv(t)
	t.Setenv("AO_CLOUD_WORKOS_LEGACY_ISSUER", "https://api.workos.com")
	t.Setenv("AO_CLOUD_WORKOS_LEGACY_CLIENT_ID", "client_staging")
	t.Setenv("AO_CLOUD_WORKOS_LEGACY_API_KEY", "sk_staging")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkOSLegacyIssuer != "https://api.workos.com/user_management/client_staging" ||
		cfg.WorkOSLegacyJWKSURL != "https://api.workos.com/sso/jwks/client_staging" ||
		cfg.WorkOSJWKSURL != "https://auth.orchestrator.inc/oauth2/jwks" {
		t.Fatalf("unexpected WorkOS config: %+v", cfg)
	}
}

func TestLoadRejectsALegacyWorkOSEnvironmentThatIsTheCurrentOne(t *testing.T) {
	setWorkOSTestEnv(t)
	t.Setenv("AO_CLOUD_WORKOS_LEGACY_ISSUER", "https://auth.orchestrator.inc")
	t.Setenv("AO_CLOUD_WORKOS_LEGACY_CLIENT_ID", "client_production")
	t.Setenv("AO_CLOUD_WORKOS_LEGACY_API_KEY", "sk_production")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("error = %v, want a must-differ error", err)
	}
}
