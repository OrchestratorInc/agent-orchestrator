package authprobe

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestCLIStatusDiagnosticsPreserveFailureWithoutRawOutput(t *testing.T) {
	for _, tc := range []struct {
		name, output, code string
		err                error
		wait               bool
	}{
		{name: "timeout", code: "probe_timeout", wait: true},
		{name: "command failed", output: "secret-token", code: "command_failed", err: errors.New("secret-token")},
		{name: "unreadable output", output: "secret-token", code: "output_unrecognized"},
		{name: "signed out", output: "not logged in secret-token", code: "cli_signed_out"},
		{name: "signed in", output: "logged in secret-token", code: "cli_signed_in"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := CmdRunner
			t.Cleanup(func() { CmdRunner = old })
			CmdRunner = func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
				if tc.wait {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				return []byte(tc.output), tc.err
			}
			status, steps, err := CLIStatusWithDiagnostics(context.Background(), "/bin/mock", [][]string{{"status"}}, time.Millisecond, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(steps) != 1 || steps[0].Code != tc.code || steps[0].Method != "mock status" {
				t.Fatalf("steps: %+v", steps)
			}
			if tc.code == "probe_timeout" && status != ports.AgentAuthStatusUnknown {
				t.Fatalf("status: %s", status)
			}
			payload, _ := json.Marshal(steps)
			if strings.Contains(string(payload), "secret-token") {
				t.Fatalf("raw output leaked: %s", payload)
			}
		})
	}
}
