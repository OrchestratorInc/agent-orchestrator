package opencode

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestOpenCodeDiagnosticsRetainInvalidCredentialStore(t *testing.T) {
	t.Setenv("OPENCODE_DATA_DIR", t.TempDir())
	path := filepath.Join(os.Getenv("OPENCODE_DATA_DIR"), "auth.json")
	if err := os.WriteFile(path, []byte(`{"provider":`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, evidence := ports.WithAuthEvidence(context.Background())
	status, _, err := opencodeStoredAuthStatus(ctx)
	if err == nil || status != ports.AgentAuthStatusUnknown {
		t.Fatalf("status=%s err=%v", status, err)
	}
	steps := evidence.Diagnostics()
	if len(steps) != 2 || steps[1].Code != "credential_file_invalid" || steps[1].NextStep == "" {
		t.Fatalf("steps=%+v", steps)
	}
}
