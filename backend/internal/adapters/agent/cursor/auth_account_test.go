package cursor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestCursorLoginAccountReadsDisplayIdentityOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cli-config.json")
	if err := os.WriteFile(path, []byte(`{"authInfo":{"email":"dev@example.com","displayName":"Dev","userId":42,"authId":"auth0|1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	want := domain.AgentAccountIdentity{Email: "dev@example.com", Name: "Dev", Method: domain.AgentAccountMethodAccount}
	if got := cursorLoginAccount(context.Background(), path); got != want {
		t.Fatalf("account = %+v, want %+v", got, want)
	}
	if got := cursorLoginAccount(context.Background(), filepath.Join(t.TempDir(), "missing.json")); got != (domain.AgentAccountIdentity{}) {
		t.Fatalf("missing config account = %+v", got)
	}
}
