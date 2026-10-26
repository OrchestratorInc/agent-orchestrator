package config

import (
	"strings"
	"testing"
)

func TestArtifactSharesOffByDefault(t *testing.T) {
	setArtifactTestEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ArtifactSharesEnabled || cfg.ArtifactShareLimit != 3 {
		t.Fatalf("shares = %v, limit = %d", cfg.ArtifactSharesEnabled, cfg.ArtifactShareLimit)
	}
}

func TestArtifactSharesOnLoads(t *testing.T) {
	setArtifactTestEnv(t)
	t.Setenv("AO_CLOUD_ARTIFACT_SHARES", "1")
	t.Setenv("AO_CLOUD_ARTIFACT_SHARE_ORIGIN", "http://share.localhost:8080/")
	t.Setenv("AO_CLOUD_ARTIFACT_SHARE_LIMIT", "5")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ArtifactSharesEnabled || cfg.ArtifactShareOrigin != "http://share.localhost:8080" || cfg.ArtifactShareLimit != 5 {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestArtifactSharesRequireSettings(t *testing.T) {
	cases := map[string]struct {
		name, value, want string
	}{
		"artifacts off":  {"AO_CLOUD_ARTIFACTS", "0", "AO_CLOUD_ARTIFACTS"},
		"missing origin": {"AO_CLOUD_ARTIFACT_SHARE_ORIGIN", "", "AO_CLOUD_ARTIFACT_SHARE_ORIGIN"},
		"bad origin":     {"AO_CLOUD_ARTIFACT_SHARE_ORIGIN", "share.localhost", "AO_CLOUD_ARTIFACT_SHARE_ORIGIN"},
		"origin path":    {"AO_CLOUD_ARTIFACT_SHARE_ORIGIN", "http://share.localhost/x", "AO_CLOUD_ARTIFACT_SHARE_ORIGIN"},
		"bad limit":      {"AO_CLOUD_ARTIFACT_SHARE_LIMIT", "0", "AO_CLOUD_ARTIFACT_SHARE_LIMIT"},
	}
	for label, tc := range cases {
		t.Run(label, func(t *testing.T) {
			setArtifactTestEnv(t)
			t.Setenv("AO_CLOUD_ARTIFACT_SHARES", "1")
			t.Setenv("AO_CLOUD_ARTIFACT_SHARE_ORIGIN", "http://share.localhost:8080")
			t.Setenv(tc.name, tc.value)
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want an error naming %s", err, tc.want)
			}
		})
	}
}

func TestArtifactSharesAreDevelopmentOnly(t *testing.T) {
	for _, environment := range []string{"staging", "production"} {
		cfg := Config{
			Environment:           environment,
			ArtifactsEnabled:      true,
			ArtifactSharesEnabled: true,
			ArtifactShareOrigin:   "https://orchestrator.inc",
		}
		if err := cfg.validateArtifactShares(); err == nil || !strings.Contains(err.Error(), "development-only") {
			t.Fatalf("%s: err = %v, want development-only refusal", environment, err)
		}
	}
}
