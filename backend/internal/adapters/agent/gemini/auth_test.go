package gemini

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestGeminiLocalAuthStatusReportsConfiguredOnlyForSelectedKey(t *testing.T) {
	for _, tc := range []struct {
		name, settings, env string
		want                ports.AgentAuthStatus
	}{
		{"selected key", `{"security":{"auth":{"selectedType":"gemini-api-key"}}}`, "GEMINI_API_KEY=sk-test\n", ports.AgentAuthStatusConfigured},
		{"OAuth selected", `{"security":{"auth":{"selectedType":"oauth-personal"}}}`, "GEMINI_API_KEY=sk-test\n", ports.AgentAuthStatusUnknown},
		{"missing key", `{"security":{"auth":{"selectedType":"gemini-api-key"}}}`, "# GEMINI_API_KEY=sk-test\n", ports.AgentAuthStatusUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, ".gemini")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(tc.settings), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(tc.env), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := geminiLocalAuthStatus(t.Context(), home)
			if err != nil || got != tc.want {
				t.Fatalf("status=%q err=%v, want %q", got, err, tc.want)
			}
		})
	}
}
