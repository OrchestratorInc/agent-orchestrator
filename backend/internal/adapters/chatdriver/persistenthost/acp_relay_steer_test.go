package persistenthost

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
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
	steerAfter := func(when string) {
		t.Helper()
		late, err := relay.clientFrame(context.Background(),
			[]byte(`{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":`+steerPromptParams+`}`+"\n"), 1)
		if err != nil || len(late.provider) != 0 || !strings.Contains(string(late.client), `"`+ACPSteerTurnEndedDataKey+`":true`) {
			t.Fatalf("steer prompt %s = provider %q client %q err %v, want refused as too late", when, late.provider, late.client, err)
		}
	}
	// The result is not acknowledged yet: the steer came too late for the turn.
	steerAfter("before the result is acknowledged")
	// Nor does it become a turn of its own once the result is acknowledged.
	ack, _ := json.Marshal(map[string]any{"method": ACPPromptAckMethod, "params": map[string]string{"eventId": relay.snapshot().PendingResultEventID}})
	relayClientFrame(t, relay, append(ack, '\n'), 1)
	if state := relay.snapshot(); state.PendingResultEventID != "" {
		t.Fatalf("state after the ack = %+v, want the result acknowledged", state)
	}
	steerAfter("after the result is acknowledged")
}

// Hermes answers a steer prompt at once and the running prompt keeps the turn,
// so the turn's result is the first prompt's later answer.
func TestACPRelaySettlesTheTurnOnTheLastPromptToReturn(t *testing.T) {
	relay := newTestACPRelay(t)
	first := startRelayPrompt(t, relay)
	steer := frameID(t, relayClientFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":`+steerPromptParams+`}`+"\n"), 1))

	ack, journaled := relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":`+steer+`,"result":{"stopReason":"end_turn"}}`+"\n"), 1, true)
	if journaled || frameID(t, ack) != "3" || strings.Contains(string(ack), ACPEventIDMetaKey) {
		t.Fatalf("acknowledged steer prompt = journaled:%v frame:%s, want a plain answer to client id 3", journaled, ack)
	}
	if state := relay.snapshot(); !state.ActivePrompt || state.PendingResultEventID != "" {
		t.Fatalf("state after the acknowledgement = %+v, want the first prompt still running", state)
	}
	final, journaled := relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":`+first+`,"result":{"stopReason":"end_turn"}}`+"\n"), 1, true)
	if !journaled || frameID(t, final) != "2" || !strings.Contains(string(final), ACPEventIDMetaKey) {
		t.Fatalf("first prompt answer = journaled:%v frame:%s, want the turn's result", journaled, final)
	}
}

// A controller that reattached mid-turn steers it with a prompt of its own. The
// earlier attachment's prompt handing off reaches it as a notification, and its
// own steer prompt's answer is the turn's result.
func TestACPRelayTellsAReattachedControllerAboutAHandoff(t *testing.T) {
	relay := newTestACPRelay(t)
	first := startRelayPrompt(t, relay)
	steer := frameID(t, relayClientFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":7,"method":"session/prompt","params":`+steerPromptParams+`}`+"\n"), 2))

	handoff, journaled := relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":`+first+`,"result":{"stopReason":"end_turn"}}`+"\n"), 2, true)
	var notification struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Result  json.RawMessage `json:"result"`
			EventID string          `json:"eventId"`
		} `json:"params"`
	}
	if err := json.Unmarshal(handoff, &notification); err != nil {
		t.Fatalf("decode handoff %q: %v", handoff, err)
	}
	if journaled || notification.Method != ACPPromptHandoffMethod || len(notification.ID) != 0 ||
		notification.Params.EventID != "" || !strings.Contains(string(notification.Params.Result), "end_turn") {
		t.Fatalf("earlier attachment's handoff = journaled:%v frame:%s, want a handoff notification without a receipt", journaled, handoff)
	}
	if !relay.snapshot().ActivePrompt {
		t.Fatal("the turn ended at the handoff")
	}
	final, journaled := relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":`+steer+`,"result":{"stopReason":"end_turn"}}`+"\n"), 2, true)
	if !journaled || frameID(t, final) != "7" || !strings.Contains(string(final), ACPEventIDMetaKey) {
		t.Fatalf("steer prompt answer = journaled:%v frame:%s, want the turn's result for client id 7", journaled, final)
	}
}

// The stall watchdog answers every prompt of a steered turn, so the controller
// is not left waiting on the one that started it.
func TestACPRelayStallWatchdogAnswersEveryPromptOfASteeredTurn(t *testing.T) {
	relay, _ := startStallTestPrompt(t)
	relayClientFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":4,"method":"session/prompt","params":{"sessionId":"session-live","prompt":[],"_meta":{"ao.steerPrompt":true}}}`+"\n"), 1)

	frames := failStalled(t, relay, 1, time.Now().Add(2*promptStallTimeout))
	answers := bytes.SplitAfter(frames.client, []byte{'\n'})
	if len(answers) != 3 || len(answers[2]) != 0 {
		t.Fatalf("watchdog client frames = %q, want one answer per prompt", frames.client)
	}
	if frameID(t, answers[0]) != "3" || !strings.Contains(string(answers[0]), "stopped responding") ||
		strings.Contains(string(answers[0]), ACPEventIDMetaKey) {
		t.Fatalf("first prompt's answer = %s, want a failure that is not the turn's result", answers[0])
	}
	if frameID(t, answers[1]) != "4" || !strings.Contains(string(answers[1]), ACPEventIDMetaKey) {
		t.Fatalf("steer prompt's answer = %s, want the turn's result", answers[1])
	}
	if !strings.Contains(string(frames.provider), "session/cancel") {
		t.Fatalf("watchdog provider frame = %q, want one session/cancel", frames.provider)
	}
	if state := relay.snapshot(); state.ActivePrompt || state.PendingResultEventID == "" {
		t.Fatalf("state after the watchdog = %+v, want the turn settled", state)
	}
}

const steerCommandParams = `{"sessionId":"s","prompt":[{"type":"text","text":"/steer use staging"}],"_meta":{"ao.steerPrompt":true,"ao.steerKeepsTurn":true}}`

// A steer command's answer is never the turn's result. The running prompt's
// answer is, success or failure, even while the command is still pending, and
// the command's later answer goes to its caller without touching the turn.
func TestACPRelayNeverSettlesTheTurnOnASteerCommand(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer string
		failed bool
	}{
		{"the turn's prompt succeeds", `"result":{"stopReason":"end_turn"}`, false},
		{"the turn's prompt fails", `"error":{"code":-32603,"message":"model request failed"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			relay := newTestACPRelay(t)
			first := startRelayPrompt(t, relay)
			steer := frameID(t, relayClientFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":`+steerCommandParams+`}`+"\n"), 1))

			final, journaled := relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":`+first+`,`+tc.answer+`}`+"\n"), 1, true)
			if !journaled || frameID(t, final) != "2" || !strings.Contains(string(final), ACPEventIDMetaKey) ||
				strings.Contains(string(final), `"error"`) != tc.failed {
				t.Fatalf("turn's prompt answer = journaled:%v frame:%s, want the turn's result", journaled, final)
			}
			state := relay.snapshot()
			if state.ActivePrompt || state.PendingResultEventID == "" {
				t.Fatalf("state after the turn's prompt = %+v, want the turn settled", state)
			}
			ack, journaled := relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":`+steer+`,"result":{"stopReason":"end_turn"}}`+"\n"), 1, true)
			if journaled || frameID(t, ack) != "3" || strings.Contains(string(ack), ACPEventIDMetaKey) {
				t.Fatalf("steer command answer = journaled:%v frame:%s, want a plain answer to client id 3", journaled, ack)
			}
			if after := relay.snapshot(); after.PendingResultEventID != state.PendingResultEventID || after.ActivePrompt {
				t.Fatalf("state after the steer command's answer = %+v, want the turn's result unchanged", after)
			}
		})
	}
}

// The stall watchdog answers a pending steer command first, so the turn's own
// prompt's failure is its result.
func TestACPRelayStallWatchdogEndsTheTurnOnItsOwnPrompt(t *testing.T) {
	relay, _ := startStallTestPrompt(t)
	relayClientFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":4,"method":"session/prompt","params":{"sessionId":"session-live","prompt":[],"_meta":{"ao.steerPrompt":true,"ao.steerKeepsTurn":true}}}`+"\n"), 1)

	frames := failStalled(t, relay, 1, time.Now().Add(2*promptStallTimeout))
	answers := bytes.SplitAfter(frames.client, []byte{'\n'})
	if len(answers) != 3 || len(answers[2]) != 0 {
		t.Fatalf("watchdog client frames = %q, want one answer per prompt", frames.client)
	}
	if frameID(t, answers[0]) != "4" || strings.Contains(string(answers[0]), ACPEventIDMetaKey) {
		t.Fatalf("steer command's answer = %s, want a failure that is not the turn's result", answers[0])
	}
	if frameID(t, answers[1]) != "3" || !strings.Contains(string(answers[1]), ACPEventIDMetaKey) {
		t.Fatalf("turn's prompt answer = %s, want the turn's result", answers[1])
	}
}
