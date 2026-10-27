package kimi

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestKimiAuthRecordsTheOAuthCredentialFile(t *testing.T) {
	clearKimiAuthEnv(t)
	home := t.TempDir()
	t.Setenv("KIMI_SHARE_DIR", t.TempDir())
	t.Setenv("KIMI_CODE_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(`
[providers."managed:kimi-code"]
type = "kimi"
oauth = { storage = "file", key = "oauth/kimi-code" }
`), 0o600); err != nil {
		t.Fatal(err)
	}
	credentials := filepath.Join(home, "credentials", "kimi-code.json")
	if err := os.MkdirAll(filepath.Dir(credentials), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credentials, []byte(`{"refresh_token":"token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, evidence := ports.WithAuthEvidence(context.Background())
	if status, ok, err := kimiLocalAuthStatus(ctx); err != nil || !ok || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = (%q, %v, %v)", status, ok, err)
	}
	want := authprobe.FileSource(credentials).WithMethod(domain.AgentAccountMethodAccount)
	if got := evidence.Identity(); got != want {
		t.Fatalf("evidence = %+v, want %+v", got, want)
	}
}
