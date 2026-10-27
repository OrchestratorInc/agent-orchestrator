package deepseekharness

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestBrowserAccountCredentialReadiness(t *testing.T) {
	t.Setenv(deepseekCredentialKey, "")
	const account = "version: 1\nrecords:\n  deepseek-account-platform/default:\n    kind: grant\n    payload:\n      version: 1\n      token: account-secret\n      issuer: https://platform.deepseek.com\n"
	for _, tc := range []struct {
		name, store string
		want        ports.AgentAuthStatus
	}{
		{"browser account", account, ports.AgentAuthStatusAuthorized},
		{"device alone", strings.Replace(account, "/default:", "/device:", 1), ports.AgentAuthStatusUnknown},
		{"empty token", strings.Replace(account, "account-secret", `""`, 1), ports.AgentAuthStatusUnknown},
		{"mock token", strings.Replace(account, "account-secret", "dsh_mock_example", 1), ports.AgentAuthStatusUnknown},
		{"wrong issuer", strings.Replace(account, "https://platform.deepseek.com", "https://example.com", 1), ports.AgentAuthStatusUnknown},
		{"wrong grant version", strings.Replace(account, "      version: 1", "      version: 2", 1), ports.AgentAuthStatusUnknown},
		{"signed out", "version: 1\nrecords: {}\n", ports.AgentAuthStatusUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".credentials.yaml")
			if err := os.WriteFile(path, []byte(tc.store), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(credentialsPathEnv, path)
			ctx, evidence := ports.WithAuthEvidence(context.Background())
			status, err := (&Plugin{resolvedBinary: "dsh"}).AuthStatus(ctx)
			if err != nil || status != tc.want {
				t.Fatalf("status = %q, err = %v, want %q", status, err, tc.want)
			}
			if status == ports.AgentAuthStatusAuthorized && evidence.Identity().Method != domain.AgentAccountMethodAccount {
				t.Fatalf("browser login must be account evidence: %+v", evidence.Identity())
			}
		})
	}
}
