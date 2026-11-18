package codexappserver

import (
	"strings"
	"testing"

	codexagent "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codex"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/codexappserver/codexproto"
)

// Codex refuses a model the ChatGPT account cannot use only on the first turn.
// The failure keeps Codex's words and adds the next step. Only the live
// conversation, which knows its account, records the refusal (#4518): a turn
// read back from history records nothing.
func TestCodexProviderFailureExplainsAModelTheAccountCannotUse(t *testing.T) {
	codexagent.ConfigureUnavailableModels("")
	t.Cleanup(func() { codexagent.ConfigureUnavailableModels("") })

	refusal := "The 'gpt-5.2-codex' model is not supported when using Codex with a ChatGPT account."
	err := codexProviderFailure(codexproto.TurnError{Message: refusal})
	if err == nil {
		t.Fatal("codexProviderFailure() = nil, want the refusal")
	}
	if !strings.Contains(err.Error(), refusal) || !strings.Contains(err.Error(), codexagent.UnavailableModelHint) {
		t.Fatalf("failure = %q, want Codex's refusal and the hint", err)
	}
	if codexagent.DefaultUnavailableModels().Unavailable("", "gpt-5.2-codex") {
		t.Fatal("codexProviderFailure recorded the refusal without knowing its account")
	}

	other := codexProviderFailure(codexproto.TurnError{Message: "stream disconnected before completion"})
	if other == nil || strings.Contains(other.Error(), codexagent.UnavailableModelHint) {
		t.Fatalf("unrelated failure = %v, want it unchanged", other)
	}
}
