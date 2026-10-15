package pi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestPiDiagnosticsExplainUnavailableLocalCredentials(t *testing.T) {
	t.Setenv("PI_DIAGNOSTIC_MISSING_KEY", "")
	for _, tc := range []struct {
		name, content, code string
		missing, unreadable bool
	}{
		{name: "missing", missing: true, code: "credential_file_missing"},
		{name: "unreadable", unreadable: true, code: "credential_store_unreadable"},
		{name: "empty", code: "credential_file_empty"},
		{name: "malformed", content: `{"key": "secret-token"`, code: "credential_file_invalid"},
		{name: "environment reference", content: `{"provider":{"type":"api_key","key":"$PI_DIAGNOSTIC_MISSING_KEY"}}`, code: "credential_reference_unresolved"},
		{name: "deferred command", content: `{"provider":{"type":"api_key","key":"!echo secret-token"}}`, code: "credential_command_deferred"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "auth.json")
			if tc.unreadable {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			} else if !tc.missing {
				if err := os.WriteFile(path, []byte(tc.content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, evidence := ports.WithAuthEvidence(context.Background())
			status, _, _, _ := piAuthJSON(ctx, path)
			if status != ports.AgentAuthStatusUnknown {
				t.Fatalf("status=%s", status)
			}
			found := false
			for _, step := range evidence.Diagnostics() {
				if step.Code == tc.code && strings.HasSuffix(step.Source, string(filepath.Separator)+"auth.json") && step.NextStep != "" {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing %s: %+v", tc.code, evidence.Diagnostics())
			}
			payload, _ := json.Marshal(evidence.Diagnostics())
			if strings.Contains(string(payload), "secret-token") {
				t.Fatal("credential contents leaked")
			}
		})
	}
}
