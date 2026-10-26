package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func setArtifactTestEnv(t *testing.T) {
	t.Helper()
	setWorkOSTestEnv(t)
	t.Setenv("AO_CLOUD_ARTIFACTS", "1")
	t.Setenv("AO_CLOUD_ARTIFACT_BUCKET", "ao-cloud-local-tenant-artifacts")
	t.Setenv("AO_CLOUD_ARTIFACT_REGION", "us-east-1")
	t.Setenv("AO_CLOUD_PROVIDER_SECRET_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
}

func TestArtifactsOffByDefault(t *testing.T) {
	setWorkOSTestEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ArtifactsEnabled {
		t.Fatal("artifacts are on by default")
	}
}

func TestArtifactsOffIgnoresMissingBucket(t *testing.T) {
	setWorkOSTestEnv(t)
	t.Setenv("AO_CLOUD_ARTIFACTS", "0")
	if _, err := Load(); err != nil {
		t.Fatalf("flag off with no bucket: %v", err)
	}
}

func TestArtifactsOnLoads(t *testing.T) {
	setArtifactTestEnv(t)
	t.Setenv("AO_CLOUD_ARTIFACT_ENDPOINT", "http://minio:9000/")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ArtifactsEnabled || cfg.ArtifactBucket != "ao-cloud-local-tenant-artifacts" ||
		cfg.ArtifactRegion != "us-east-1" || cfg.ArtifactEndpoint != "http://minio:9000" {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestArtifactsOnRequiresSettings(t *testing.T) {
	cases := map[string]struct {
		name, value, want string
	}{
		"bucket":     {"AO_CLOUD_ARTIFACT_BUCKET", "", "AO_CLOUD_ARTIFACT_BUCKET"},
		"region":     {"AO_CLOUD_ARTIFACT_REGION", "", "AO_CLOUD_ARTIFACT_REGION"},
		"secret key": {"AO_CLOUD_PROVIDER_SECRET_KEY", "", "AO_CLOUD_PROVIDER_SECRET_KEY"},
		"endpoint":   {"AO_CLOUD_ARTIFACT_ENDPOINT", "minio:9000", "AO_CLOUD_ARTIFACT_ENDPOINT"},
	}
	for label, tc := range cases {
		t.Run(label, func(t *testing.T) {
			setArtifactTestEnv(t)
			t.Setenv(tc.name, tc.value)
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want an error naming %s", err, tc.want)
			}
		})
	}
}

func TestArtifactsRejectsAnUnreadableFlag(t *testing.T) {
	setWorkOSTestEnv(t)
	t.Setenv("AO_CLOUD_ARTIFACTS", "yes please")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "AO_CLOUD_ARTIFACTS") {
		t.Fatalf("Load with AO_CLOUD_ARTIFACTS=%q err = %v, want an error", "yes please", err)
	}
}
