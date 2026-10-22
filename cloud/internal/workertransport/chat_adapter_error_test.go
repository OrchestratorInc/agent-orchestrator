package workertransport

import (
	"fmt"
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
