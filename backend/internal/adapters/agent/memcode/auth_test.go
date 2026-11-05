package memcode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestMemcodeAuthStatusFromNativeOutput(t *testing.T) {
	tests := []struct {
		name, output string
		want         ports.AgentAuthStatus
	}{
		{"saved subscription active", "  Lane: claude subscription → anthropic models (active)\n", ports.AgentAuthStatusConfigured},
		{"saved subscription missing login", "  Lane: claude subscription → anthropic models (attached, but NOT signed in — run `memcode auth claude`)\n", ports.AgentAuthStatusUnauthorized},
		{"hosted account", "  Signed in to a memcode account (serves every model family).\n", ports.AgentAuthStatusConfigured},
		{"custom endpoint", "  Using a custom endpoint: http://127.0.0.1:11434/v1\n", ports.AgentAuthStatusConfigured},
		{"provider key", "  Using your exported ANTHROPIC_API_KEY.\n", ports.AgentAuthStatusConfigured},
		{"disconnected", "  Not connected yet. Run `memcode auth` to set it up.\n", ports.AgentAuthStatusUnauthorized},
		{"unrecognized", "unexpected output", ports.AgentAuthStatusUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := memcodeAuthStatusFromText(tt.output); got != tt.want {
				t.Fatalf("status = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReadMemcodeAuthEnvLoadsOnlyNativeAuthSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	contents := "# native login\nMEMCODE_CREDENTIAL_SOURCE=\nMEMCODE_CREDENTIALS=claude\nMEMCODE_API_TOKEN='secret-token'\nUNRELATED_SECRET=do-not-forward\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err := readMemcodeAuthEnv(path)
	if err != nil {
		t.Fatal(err)
	}
	if settings["MEMCODE_CREDENTIALS"] != "claude" || settings["MEMCODE_API_TOKEN"] != "secret-token" {
		t.Fatalf("expected saved native auth settings, got credentials=%q tokenPresent=%t", settings["MEMCODE_CREDENTIALS"], settings["MEMCODE_API_TOKEN"] != "")
	}
	if _, ok := settings["UNRELATED_SECRET"]; ok {
		t.Fatal("unrelated secret was read into probe environment")
	}
}
