package authprobe

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestStatusFromTextReadsANullAccountAsSignedOut(t *testing.T) {
	for _, out := range []string{
		`{"account":null}`,
		`{"account": null}`,
		"{\n  \"account\": null\n}\n",
	} {
		if got := StatusFromText(out); got != ports.AgentAuthStatusUnauthorized {
			t.Errorf("StatusFromText(%q) = %q, want unauthorized", out, got)
		}
	}
}

func TestStatusFromTextLeavesAPopulatedAccountAlone(t *testing.T) {
	if got := StatusFromText(`{"account":{"id":"abc"}}`); got == ports.AgentAuthStatusUnauthorized {
		t.Fatalf("StatusFromText = %q, want anything but unauthorized", got)
	}
}
