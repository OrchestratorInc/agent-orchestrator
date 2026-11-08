package codexappserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A reviewer is read-only with approvals disabled, so the sandbox probe must
// use exactly that posture: the same policy the turn applies, with no approvals
// and no network.
func TestProbeSandboxRunsNoopCommandInTheReadOnlyPosture(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.respondTo("command/exec", `{"exitCode":0,"stdout":"","stderr":""}`)
	workspace := t.TempDir()

	if err := d.ProbeSandbox(context.Background(), ports.ChatSandboxProbeConfig{WorkspacePath: workspace}); err != nil {
		t.Fatalf("ProbeSandbox: %v", err)
	}

	f := srv.awaitFrame(func(f frame) bool { return f.Method == "command/exec" })
	var params struct {
		Command []string `json:"command"`
		Cwd     string   `json:"cwd"`
		Policy  struct {
			Type          string `json:"type"`
			NetworkAccess bool   `json:"networkAccess"`
		} `json:"sandboxPolicy"`
	}
	if err := json.Unmarshal(f.Params, &params); err != nil {
		t.Fatalf("decode command/exec params: %v", err)
	}
	if params.Cwd != workspace {
		t.Fatalf("cwd = %q, want %q", params.Cwd, workspace)
	}
	if params.Policy.Type != "readOnly" || params.Policy.NetworkAccess {
		t.Fatalf("sandboxPolicy = %+v, want readOnly without network", params.Policy)
	}
	want := sandboxProbeCommand()
	if len(params.Command) != len(want) {
		t.Fatalf("command = %#v, want %#v", params.Command, want)
	}
	for i := range want {
		if params.Command[i] != want[i] {
			t.Fatalf("command = %#v, want %#v", params.Command, want)
		}
	}
}

// The reported failure: bwrap cannot create its user namespace, so the command
// never starts. The provider's own error must reach the caller, and it must be
// identifiable as a sandbox problem rather than a generic protocol failure.
func TestProbeSandboxReportsTheProvidersSandboxError(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.replyError("command/exec", -32603, "bwrap: setting up uid map: Permission denied")

	err := d.ProbeSandbox(context.Background(), ports.ChatSandboxProbeConfig{WorkspacePath: t.TempDir()})
	if !errors.Is(err, ports.ErrChatSandboxUnavailable) {
		t.Fatalf("err = %v, want ErrChatSandboxUnavailable", err)
	}
	if !strings.Contains(err.Error(), "bwrap: setting up uid map: Permission denied") {
		t.Fatalf("err = %v, want the provider's message preserved", err)
	}
}

// Some providers report a blocked sandbox as a failed process instead of a
// JSON-RPC error. That must fail the probe too, with the captured output.
func TestProbeSandboxRejectsANonZeroProbeExit(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.respondTo("command/exec", `{"exitCode":1,"stdout":"","stderr":"sandbox setup failed"}`)

	err := d.ProbeSandbox(context.Background(), ports.ChatSandboxProbeConfig{WorkspacePath: t.TempDir()})
	if !errors.Is(err, ports.ErrChatSandboxUnavailable) {
		t.Fatalf("err = %v, want ErrChatSandboxUnavailable", err)
	}
	if !strings.Contains(err.Error(), "sandbox setup failed") {
		t.Fatalf("err = %v, want captured stderr", err)
	}
}

// A provider that predates command/exec cannot answer the probe. Launching the
// review unchanged is better than blocking every review on an unsupported
// preflight.
func TestProbeSandboxSkipsProvidersWithoutCommandExec(t *testing.T) {
	d, srv := newTestDriver(t)
	srv.replyError("command/exec", -32601, "method not found")

	if err := d.ProbeSandbox(context.Background(), ports.ChatSandboxProbeConfig{WorkspacePath: t.TempDir()}); err != nil {
		t.Fatalf("ProbeSandbox = %v, want nil for an unsupported probe", err)
	}
}
