package persistenthost

import (
	"context"
	"strings"
	"testing"
)

const steerPromptParams = `{"sessionId":"s","prompt":[{"type":"text","text":"use staging"}],"_meta":{"ao.steerPrompt":true}}`

// startRelayPrompt opens a session and starts one prompt, returning its
// provider-side id.
func startRelayPrompt(t *testing.T, relay *acpRelay) string {
	t.Helper()
	session := relayClientFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":1,"method":"session/new","params":{"cwd":"/tmp","mcpServers":[]}}`+"\n"), 1)
	relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":`+frameID(t, session)+`,"result":{"sessionId":"s"}}`+"\n"), 1, true)
	prompt := relayClientFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":2,"method":"session/prompt","params":{"sessionId":"s","prompt":[]}}`+"\n"), 1)
	return frameID(t, prompt)
}

func TestACPRelayAdmitsOnlyAMarkedSteerPromptIntoTheActiveTurn(t *testing.T) {
	relay := newTestACPRelay(t)
	startRelayPrompt(t, relay)

	plain, err := relay.clientFrame(context.Background(),
		[]byte(`{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":"s","prompt":[]}}`+"\n"), 1)
	if err != nil || len(plain.provider) != 0 || !strings.Contains(string(plain.client), "previous ACP prompt is active") {
		t.Fatalf("unmarked second prompt = provider %q client %q err %v, want refused", plain.provider, plain.client, err)
	}
	steer := relayClientFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":4,"method":"session/prompt","params":`+steerPromptParams+`}`+"\n"), 1)
	if len(steer) == 0 {
		t.Fatal("marked steer prompt was not forwarded to the agent")
	}
	if !relay.snapshot().ActivePrompt {
		t.Fatal("the turn stopped being active when the steer prompt joined it")
	}
}

func TestACPRelaySettlesTheTurnOnTheSteerPrompt(t *testing.T) {
	relay := newTestACPRelay(t)
	first := startRelayPrompt(t, relay)
	update, _ := relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"working"}}}}`+"\n"), 1, true)
	steer := frameID(t, relayClientFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":`+steerPromptParams+`}`+"\n"), 1))

	// The agent hands the first prompt off: its answer is not the turn's result.
	handoff, journaled := relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":`+first+`,"result":{"stopReason":"end_turn"}}`+"\n"), 1, true)
	if journaled || frameID(t, handoff) != "2" || strings.Contains(string(handoff), ACPEventIDMetaKey) {
		t.Fatalf("handed-off answer = journaled:%v frame:%s, want a plain answer to client id 2", journaled, handoff)
	}
	state := relay.snapshot()
	if !state.ActivePrompt || state.PendingResultEventID != "" {
		t.Fatalf("state after handoff = %+v, want the turn still active", state)
	}
	// The turn's journal survives the steer prompt joining it.
	if replay := relayReplayFrames(t, relay); len(replay) != 1 || !strings.Contains(string(replay[0]), "working") {
		t.Fatalf("replay after handoff = %q (update was %s)", replay, update)
	}

	final, journaled := relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":`+steer+`,"result":{"stopReason":"end_turn"}}`+"\n"), 1, true)
	if !journaled || frameID(t, final) != "3" || !strings.Contains(string(final), ACPEventIDMetaKey) {
		t.Fatalf("steer prompt answer = journaled:%v frame:%s, want the turn's result", journaled, final)
	}
	state = relay.snapshot()
	if state.ActivePrompt || state.PendingResultEventID == "" {
		t.Fatalf("state after the steer prompt = %+v, want the turn settled", state)
	}
}

func TestACPRelayRefusedSteerPromptLeavesTheTurnToTheFirstPrompt(t *testing.T) {
	relay := newTestACPRelay(t)
	first := startRelayPrompt(t, relay)
	steer := frameID(t, relayClientFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":`+steerPromptParams+`}`+"\n"), 1))

	refusal, journaled := relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":`+steer+`,"error":{"code":-32600,"message":"a prompt is already running"}}`+"\n"), 1, true)
	if journaled || frameID(t, refusal) != "3" || !strings.Contains(string(refusal), "already running") {
		t.Fatalf("refusal = journaled:%v frame:%s, want it passed to client id 3", journaled, refusal)
	}
	if state := relay.snapshot(); !state.ActivePrompt || state.PendingResultEventID != "" {
		t.Fatalf("state after refusal = %+v, want the first prompt still running", state)
	}
	final, journaled := relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":`+first+`,"result":{"stopReason":"end_turn"}}`+"\n"), 1, true)
	if !journaled || frameID(t, final) != "2" || !strings.Contains(string(final), ACPEventIDMetaKey) {
		t.Fatalf("first prompt answer = journaled:%v frame:%s, want the turn's result", journaled, final)
	}
}

func TestACPRelayRefusesASteerPromptOutsideATurn(t *testing.T) {
	relay := newTestACPRelay(t)
	first := startRelayPrompt(t, relay)
	relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":`+first+`,"result":{"stopReason":"end_turn"}}`+"\n"), 1, true)
	// The result is not acknowledged yet, so even a steer prompt must wait.
	late, err := relay.clientFrame(context.Background(),
		[]byte(`{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":`+steerPromptParams+`}`+"\n"), 1)
	if err != nil || len(late.provider) != 0 || !strings.Contains(string(late.client), "not acknowledged") {
		t.Fatalf("steer prompt after the turn = provider %q client %q err %v, want refused", late.provider, late.client, err)
	}
}
