package grok

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestGrokAuthRecordsTheStoredLoginFile(t *testing.T) {
	grokHome := t.TempDir()
	t.Setenv("GROK_HOME", grokHome)
	t.Setenv("XAI_API_KEY", "")
	path := filepath.Join(grokHome, "auth.json")
	if err := os.WriteFile(path, []byte(`{"xai":{"refresh_token":"token"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, evidence := ports.WithAuthEvidence(context.Background())
	status, ok, err := grokLocalAuthStatus(ctx)
	if err != nil || !ok || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = (%q, %v, %v)", status, ok, err)
	}
	want := authprobe.FileSource(path).WithMethod(domain.AgentAccountMethodAccount)
	if got := evidence.Identity(); got != want {
		t.Fatalf("evidence = %+v, want %+v", got, want)
	}
}

func TestGrokAuthNamesTheSignedInXAIUser(t *testing.T) {
	grokHome := t.TempDir()
	t.Setenv("GROK_HOME", grokHome)
	t.Setenv("XAI_API_KEY", "")
	path := filepath.Join(grokHome, "auth.json")
	body := `{"https://auth.x.ai::client":{"key":"jwt","refresh_token":"token","email":"dev@example.com","first_name":"Ada","last_name":"Lovelace"}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, evidence := ports.WithAuthEvidence(context.Background())
	if _, ok, err := grokLocalAuthStatus(ctx); err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	got := evidence.Identity()
	if got.Email != "dev@example.com" || got.Name != "Ada Lovelace" || got.Method != domain.AgentAccountMethodAccount {
		t.Fatalf("evidence = %+v", got)
	}
}
