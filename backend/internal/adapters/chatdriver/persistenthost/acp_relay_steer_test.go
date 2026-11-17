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

// agy ends the first prompt only when the steer prompt takes the work over, so
// once it has handed off, the steer prompt's failure is the turn's failure.
func TestACPRelayHandedOffTurnFailsWhenTheSteerPromptFails(t *testing.T) {
	relay := newTestACPRelay(t)
	first := startRelayPrompt(t, relay)
	steer := frameID(t, relayClientFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":`+steerPromptParams+`}`+"\n"), 1))

	handoff, journaled := relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":`+first+`,"result":{"stopReason":"end_turn"}}`+"\n"), 1, true)
	if journaled || frameID(t, handoff) != "2" || strings.Contains(string(handoff), ACPEventIDMetaKey) {
		t.Fatalf("handed-off answer = journaled:%v frame:%s, want a plain answer to client id 2", journaled, handoff)
	}
	failed, journaled := relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":`+steer+`,"error":{"code":-32603,"message":"model request failed"}}`+"\n"), 1, true)
	if !journaled || frameID(t, failed) != "3" || !strings.Contains(string(failed), ACPEventIDMetaKey) || !strings.Contains(string(failed), "model request failed") {
		t.Fatalf("failed steer prompt answer = journaled:%v frame:%s, want it to settle the turn", journaled, failed)
	}
	if state := relay.snapshot(); state.ActivePrompt || state.PendingResultEventID == "" {
		t.Fatalf("state after the failed steer prompt = %+v, want the turn settled", state)
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
const stalledSteerPrompt = `{"jsonrpc":"2.0","id":4,"method":"session/prompt","params":{"sessionId":"session-live","prompt":[],"_meta":{"ao.steerPrompt":true}}}` + "\n"

// A steered turn that stalls has two prompts in flight. The watchdog cancels
// the turn once, and the adapter's answers settle it: the earlier answer passes
// through as it is, and only the last one, after which the adapter is quiet,
// becomes the stall failure that ends the turn.
func TestACPRelayStalledSteeredTurnEndsOnceWhenTheAdapterAnswersTheCancel(t *testing.T) {
	relay, root := startStallTestPrompt(t)
	steer := frameID(t, relayClientFrame(t, relay, []byte(stalledSteerPrompt), 1))

	stalledAt := time.Now().Add(2 * promptStallTimeout)
	first := failStalled(t, relay, 1, stalledAt)
	if want := `{"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":"session-live"}}` + "\n"; string(first.provider) != want {
		t.Fatalf("adapter frame = %s, want one session/cancel", first.provider)
	}
	if first.client != nil || !relay.snapshot().ActivePrompt {
		t.Fatalf("watchdog settled the turn before the adapter answered the cancel: %+v", first)
	}

	rootAnswer, journaled := relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":`+root+`,"result":{"stopReason":"cancelled"}}`+"\n"), 1, true)
	if journaled || frameID(t, rootAnswer) != "3" || strings.Contains(string(rootAnswer), ACPPromptStalledDataKey) ||
		strings.Contains(string(rootAnswer), ACPEventIDMetaKey) {
		t.Fatalf("root prompt's answer = %s, want it passed through, not the turn's result", rootAnswer)
	}
	if !relay.snapshot().ActivePrompt {
		t.Fatal("the turn ended while its steer prompt was still running")
	}

	steerAnswer, _ := relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":`+steer+`,"result":{"stopReason":"cancelled"}}`+"\n"), 1, true)
	if frameID(t, steerAnswer) != "4" || !strings.Contains(string(steerAnswer), `"`+ACPPromptStalledDataKey+`":true`) ||
		!strings.Contains(string(steerAnswer), ACPEventIDMetaKey) {
		t.Fatalf("last prompt's answer = %s, want the stall failure as the turn's result", steerAnswer)
	}
	if state := relay.snapshot(); state.ActivePrompt || state.PendingResultEventID == "" {
		t.Fatalf("state after the last answer = %+v, want the turn settled", state)
	}
	if frames := failStalled(t, relay, 1, stalledAt.Add(2*promptStallCancelGrace)); frames.client != nil || frames.provider != nil {
		t.Fatalf("watchdog acted after the adapter answered every prompt: %+v", frames)
	}
	if len(relay.abandonedPrompts) != 0 || len(relay.pending) != 0 {
		t.Fatalf("prompts left behind: abandoned %v pending %v", relay.abandonedPrompts, relay.pending)
	}
}

// When the adapter ignores the cancel, the watchdog settles every prompt still
// waiting once the grace runs out, oldest first, in one pass. None carries the
// stall marker, so the controller keeps its queue held.
func TestACPRelayStalledSteeredTurnSettlesEveryPromptWhenTheCancelIsIgnored(t *testing.T) {
	relay, _ := startStallTestPrompt(t)
	relayClientFrame(t, relay, []byte(stalledSteerPrompt), 1)

	stalledAt := time.Now().Add(2 * promptStallTimeout)
	failStalled(t, relay, 1, stalledAt)
	if frames := failStalled(t, relay, 1, stalledAt.Add(promptStallCancelGrace/2)); frames.client != nil || frames.provider != nil {
		t.Fatalf("watchdog acted again inside the cancel grace: %+v", frames)
	}
	frames := failStalled(t, relay, 1, stalledAt.Add(promptStallCancelGrace))
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
	if strings.Contains(string(frames.client), ACPPromptStalledDataKey) || frames.provider != nil {
		t.Fatalf("wedged-adapter settlement = client %s provider %s, want no stall marker and no second cancel",
			frames.client, frames.provider)
	}
	if state := relay.snapshot(); state.ActivePrompt || state.PendingResultEventID == "" {
		t.Fatalf("state after the watchdog = %+v, want the turn settled", state)
	}
	if len(relay.abandonedPrompts) != 2 {
		t.Fatalf("abandoned prompts = %v, want both, so their late answers are dropped", relay.abandonedPrompts)
	}
}

// Once the turn is being cancelled, by Stop or by the watchdog, a steer prompt
// is refused at the host: the agent would run it as fresh work after the cancel.
func TestACPRelayRefusesASteerPromptOnceTheTurnIsBeingCancelled(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cancel func(t *testing.T, relay *acpRelay)
	}{
		{"Stop's cancel", func(t *testing.T, relay *acpRelay) {
			relayClientFrame(t, relay, []byte(`{"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":"session-live"}}`+"\n"), 1)
		}},
		{"the watchdog's cancel", func(t *testing.T, relay *acpRelay) {
			if frames := failStalled(t, relay, 1, time.Now().Add(2*promptStallTimeout)); len(frames.provider) == 0 {
				t.Fatal("watchdog did not cancel the stalled prompt")
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			relay, _ := startStallTestPrompt(t)
			tc.cancel(t, relay)
			frames, err := relay.clientFrame(context.Background(), []byte(stalledSteerPrompt), 1)
			if err != nil || len(frames.provider) != 0 || !strings.Contains(string(frames.client), ACPSteerTurnEndedDataKey) {
				t.Fatalf("steer after the cancel = provider %q client %q err %v, want refused as too late",
					frames.provider, frames.client, err)
			}
			if len(relay.pending) != 1 {
				t.Fatalf("pending = %v, want only the turn's own prompt", relay.pending)
			}
		})
	}
}

const steerCommandParams = `{"sessionId":"s","prompt":[{"type":"text","text":"/steer use staging"}],"_meta":{"ao.steerPrompt":true,"ao.steerKeepsTurn":true}}`

// A steer command's answer is never the turn's result. The turn's own prompt's
// answer is, success or failure. Answered first, the command leaves the turn
// to that prompt as usual.
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

			ack, journaled := relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":`+steer+`,"result":{"stopReason":"end_turn"}}`+"\n"), 1, true)
			if journaled || frameID(t, ack) != "3" || strings.Contains(string(ack), ACPEventIDMetaKey) {
				t.Fatalf("steer command answer = journaled:%v frame:%s, want a plain answer to client id 3", journaled, ack)
			}
			if !relay.snapshot().ActivePrompt {
				t.Fatal("the steer command's answer ended the turn")
			}
			final, journaled := relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":`+first+`,`+tc.answer+`}`+"\n"), 1, true)
			if !journaled || frameID(t, final) != "2" || !strings.Contains(string(final), ACPEventIDMetaKey) ||
				strings.Contains(string(final), `"error"`) != tc.failed {
				t.Fatalf("turn's prompt answer = journaled:%v frame:%s, want the turn's result", journaled, final)
			}
			if state := relay.snapshot(); state.ActivePrompt || state.PendingResultEventID == "" {
				t.Fatalf("state after the turn's prompt = %+v, want the turn settled", state)
			}
		})
	}
}

// Hermes runs the guidance inside its `/steer` request when the turn ended just
// before the command arrived ("No active turn"). The turn's own answer then
// waits for the command's, so the run's output stays in the turn and the next
// prompt does not reach a busy agent. The result is still the turn's own answer.
func TestACPRelayKeepsTheTurnOpenWhileASteerCommandRuns(t *testing.T) {
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

			held, journaled := relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":`+first+`,`+tc.answer+`}`+"\n"), 1, true)
			if len(held) != 0 || journaled {
				t.Fatalf("turn's prompt answer = journaled:%v frame:%s, want it held while the command runs", journaled, held)
			}
			if state := relay.snapshot(); !state.ActivePrompt || state.PendingResultEventID != "" {
				t.Fatalf("state while the command runs = %+v, want the turn still active", state)
			}
			if busy := relayClientFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":4,"method":"session/prompt","params":{"sessionId":"s","prompt":[]}}`+"\n"), 1); len(busy) != 0 {
				t.Fatalf("a new prompt reached the agent while the command ran: %s", busy)
			}
			run, _ := relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"editing staging.yaml"}}}}`+"\n"), 1, true)
			if !strings.Contains(string(run), ACPEventIDMetaKey) {
				t.Fatalf("the command's run = %s, want it journaled in the turn", run)
			}

			frames, journaled := relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":`+steer+`,"result":{"stopReason":"end_turn"}}`+"\n"), 1, true)
			answers := bytes.SplitAfter(frames, []byte{'\n'})
			if !journaled || len(answers) != 3 || len(answers[2]) != 0 {
				t.Fatalf("frames when the command answers = %q, want its answer and then the turn's result", frames)
			}
			if frameID(t, answers[0]) != "3" || strings.Contains(string(answers[0]), ACPEventIDMetaKey) {
				t.Fatalf("command answer = %s, want a plain answer to client id 3", answers[0])
			}
			if frameID(t, answers[1]) != "2" || !strings.Contains(string(answers[1]), ACPEventIDMetaKey) ||
				strings.Contains(string(answers[1]), `"error"`) != tc.failed {
				t.Fatalf("turn's result = %s, want the turn's own answer with the receipt", answers[1])
			}
			if state := relay.snapshot(); state.ActivePrompt || state.PendingResultEventID == "" {
				t.Fatalf("state after the command answered = %+v, want the turn settled", state)
			}
		})
	}
}

// The stall watchdog cancels the turn first. If the adapter answers the
// cancel, the turn's own answer, held for a running command, ends the turn
// with the stall marker once the command answers too.
func TestACPRelayStalledTurnWithARunningSteerCommandEndsOnceTheCommandAnswers(t *testing.T) {
	relay, root := startStallTestPrompt(t)
	steer := frameID(t, relayClientFrame(t, relay, []byte(stalledSteerCommand), 1))

	stalledAt := time.Now().Add(2 * promptStallTimeout)
	if first := failStalled(t, relay, 1, stalledAt); len(first.provider) == 0 || first.client != nil {
		t.Fatalf("watchdog's first step = %+v, want only a session/cancel", first)
	}
	if held, _ := relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":`+root+`,"result":{"stopReason":"cancelled"}}`+"\n"), 1, true); len(held) != 0 {
		t.Fatalf("turn's prompt answer = %s, want it held while the command runs", held)
	}
	frames, _ := relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":`+steer+`,"result":{"stopReason":"cancelled"}}`+"\n"), 1, true)
	answers := bytes.SplitAfter(frames, []byte{'\n'})
	if len(answers) != 3 || frameID(t, answers[0]) != "4" || frameID(t, answers[1]) != "3" ||
		!strings.Contains(string(answers[1]), `"`+ACPPromptStalledDataKey+`":true`) {
		t.Fatalf("frames when the command answers = %q, want its answer, then the stall failure as the turn's result", frames)
	}
	if state := relay.snapshot(); state.ActivePrompt || state.PendingResultEventID == "" {
		t.Fatalf("state after the command answered = %+v, want the turn settled", state)
	}
}

const stalledSteerCommand = `{"jsonrpc":"2.0","id":4,"method":"session/prompt","params":{"sessionId":"session-live","prompt":[],"_meta":{"ao.steerPrompt":true,"ao.steerKeepsTurn":true}}}` + "\n"

// If the adapter ignores the cancel, the watchdog answers a pending steer
// command first, so the turn's own prompt's failure is its result.
func TestACPRelayStallWatchdogEndsTheTurnOnItsOwnPrompt(t *testing.T) {
	relay, _ := startStallTestPrompt(t)
	relayClientFrame(t, relay, []byte(stalledSteerCommand), 1)

	stalledAt := time.Now().Add(2 * promptStallTimeout)
	failStalled(t, relay, 1, stalledAt)
	frames := failStalled(t, relay, 1, stalledAt.Add(promptStallCancelGrace))
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

// When the turn's own prompt has answered and only a running command is left,
// a watchdog whose cancel is ignored fails the command and then the turn. The
// held success would release the queue into a wedged adapter, so it becomes a
// failure, without the stall marker.
func TestACPRelayStallWatchdogSettlesAHeldResultWhenTheCancelIsIgnored(t *testing.T) {
	relay, root := startStallTestPrompt(t)
	relayClientFrame(t, relay, []byte(stalledSteerCommand), 1)
	if held, _ := relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":`+root+`,"result":{"stopReason":"end_turn"}}`+"\n"), 1, true); len(held) != 0 {
		t.Fatalf("turn's prompt answer = %s, want it held while the command runs", held)
	}

	stalledAt := time.Now().Add(2 * promptStallTimeout)
	failStalled(t, relay, 1, stalledAt)
	frames := failStalled(t, relay, 1, stalledAt.Add(promptStallCancelGrace))
	answers := bytes.SplitAfter(frames.client, []byte{'\n'})
	if len(answers) != 3 || frameID(t, answers[0]) != "4" || frameID(t, answers[1]) != "3" ||
		strings.Contains(string(answers[1]), `"end_turn"`) || !strings.Contains(string(answers[1]), "stopped responding") ||
		!strings.Contains(string(answers[1]), ACPEventIDMetaKey) || strings.Contains(string(frames.client), ACPPromptStalledDataKey) {
		t.Fatalf("watchdog client frames = %q, want the command's failure, then the turn failed without the marker", frames.client)
	}
	if state := relay.snapshot(); state.ActivePrompt || state.PendingResultEventID == "" {
		t.Fatalf("state after the watchdog = %+v, want the turn settled", state)
	}
}
