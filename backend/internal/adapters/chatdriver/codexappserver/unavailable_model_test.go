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

// A refusal is dated by the provider's completedAt, so a turn/completed a
// detached host replays later keeps its original time; a notification without
// that time is not recorded.
func TestTurnCompletedAtUsesTheProvidersTime(t *testing.T) {
	failed := notification{Method: codexproto.MethodTurnCompleted, Params: []byte(
		`{"threadId":"t","turn":{"id":"u","status":"failed","completedAt":1795500000,"error":{"message":"The 'gpt-5.2-codex' model is not supported when using Codex with a ChatGPT account."}}}`)}
	at, ok := turnCompletedAt(failed)
	if !ok || at.Unix() != 1795500000 {
		t.Fatalf("turnCompletedAt = %v, %v; want the provider's completedAt", at, ok)
	}
	for name, n := range map[string]notification{
		"no completedAt": {Method: codexproto.MethodTurnCompleted, Params: []byte(`{"turn":{"status":"failed","error":{"message":"x"}}}`)},
		"no error":       {Method: codexproto.MethodTurnCompleted, Params: []byte(`{"turn":{"status":"completed","completedAt":1795500000}}`)},
		"other method":   {Method: "error", Params: []byte(`{"error":{"message":"x"}}`)},
	} {
		if _, ok := turnCompletedAt(n); ok {
			t.Errorf("%s: turnCompletedAt reported a refusal time", name)
		}
	}
}
