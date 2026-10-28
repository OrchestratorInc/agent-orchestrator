package workertransport

import (
	"fmt"
	"os/exec"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/workerexec"
)

// A missing Claude ACP adapter must reach the app as a specific, actionable
// error instead of the generic "could not complete the operation".
func TestChatAdapterErrorsAreSpecific(t *testing.T) {
	cases := map[string]error{
		"CHAT_ADAPTER_INSTALLING":  workerexec.ErrChatAdapterInstalling,
		"CHAT_ADAPTER_UNAVAILABLE": fmt.Errorf("%w: npm is not installed on the workspace", workerexec.ErrChatAdapterUnavailable),
	}
	for want, err := range cases {
		if code, message := transportError(err); code != want || message == "" {
			t.Fatalf("transportError(%v) = %q, %q; want %q", err, code, message, want)
		}
	}
}

// A coding agent that is not installed yet (a template still installing it, or
// the worker's own install still running) is a transient, specific error, not
// the generic "could not complete the operation", so the app can retry it.
func TestMissingAgentBinaryIsReportedAsInstalling(t *testing.T) {
	err := exec.Command("ao-test-agent-that-is-not-installed").Start()
	if err == nil {
		t.Fatal("starting a missing binary succeeded")
	}
	if code, message := transportError(fmt.Errorf("discover models: %w", err)); code != "CHAT_AGENT_INSTALLING" || message == "" {
		t.Fatalf("transportError(%v) = %q, %q; want CHAT_AGENT_INSTALLING", err, code, message)
	}
}
