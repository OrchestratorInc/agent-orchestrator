package acp

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// startScriptedAckConversation is startScriptedHostConversation for an agent
// that acknowledges steer prompts, as Hermes does.
func startScriptedAckConversation(t *testing.T, adoptTurn string) (*conversation, *scriptedHost) {
	t.Helper()
	conv, host := startScriptedHostConversation(t, adoptTurn)
	conv.mu.Lock()
	conv.steering.prompt = testAckSteerPrompt()
	conv.mu.Unlock()
	return conv, host
}

func ackNote(text string) string {
	return `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"provider-session","update":` +
		`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"` + text + `"}}}}`
}

// The acknowledgement says the steer took. The `/steer` prompt is marked as a
// command, so the host gives the turn's result to the running prompt, and the
// turn settles with it whichever answer arrives or is routed first.
func TestACPSteerPromptAcknowledgedSteerKeepsTheHostResultInEitherOrder(t *testing.T) {
	for _, tc := range []struct {
		name          string
		steerFirst    bool
		first, second int
	}{
		{"steer answers first", true, 1, 0},
		{"steer answers first, routed in reverse", true, 0, 1},
		{"turn's prompt answers first", false, 0, 1},
		{"turn's prompt answers first, routed in reverse", false, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conv, host := startScriptedAckConversation(t, "")
			answerOrder(conv, tc.first, tc.second)
			_ = nextEvent(t, conv.Events()) // controller.ready
			turn, root := startScriptedTurn(t, conv, host)
			steer, outcome := steerAsync(t, conv, host, turn)
			var meta struct {
				Meta map[string]any `json:"_meta"`
			}
			_ = json.Unmarshal(host.lastPrompt, &meta)
			if meta.Meta[persistenthost.ACPSteerPromptMetaKey] != true || meta.Meta[persistenthost.ACPSteerKeepsTurnMetaKey] != true {
				t.Fatalf("steer prompt _meta = %v, want it marked as a steer command", meta.Meta)
			}

			host.send(ackNote("ACK: queued for the active turn"))
			if tc.steerFirst {
				host.send(plainResult(steer))
				host.send(receiptResult(root, "acp-host:7"))
			} else {
				host.send(receiptResult(root, "acp-host:7"))
				host.send(plainResult(steer))
			}
			if err := steerOutcome(t, outcome); err != nil {
				t.Fatalf("Steer = %v, want delivered", err)
			}
			done := completionsOf(t, conv, turn)
			if len(done) != 1 || done[0].TurnState != domain.TurnStateCompleted || done[0].ProviderEventID != "acp-host:7" {
				t.Fatalf("turn completions = %+v, want one completed with the host's receipt", done)
			}
		})
	}
}

// A declined acknowledgement stays a refusal however the turn's prompts then
// answer: another prompt finishing says nothing about this steer.
func TestACPSteerPromptDeclinedAcknowledgementStaysARefusal(t *testing.T) {
	for _, tc := range []struct {
		name string
		// rootFirst sends the turn's own prompt's answer, which carries the
		// host's receipt, before the steer prompt's.
		rootFirst bool
		// noteLate sends the note after the turn's own prompt has answered.
		noteLate      bool
		first, second int
	}{
		{"the turn's prompt answers first", true, false, 0, 1},
		{"the turn's prompt answers first, routed in reverse", true, false, 1, 0},
		{"the turn's prompt answers before the note", true, true, 0, 1},
		{"the steer prompt answers first", false, false, 1, 0},
		{"the steer prompt answers first, routed in reverse", false, false, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conv, host := startScriptedAckConversation(t, "")
			answerOrder(conv, tc.first, tc.second)
			_ = nextEvent(t, conv.Events()) // controller.ready
			turn, root := startScriptedTurn(t, conv, host)
			steer, outcome := steerAsync(t, conv, host, turn)

			if !tc.noteLate {
				host.send(ackNote("DECLINED: the turn is compacting"))
			}
			if tc.rootFirst {
				host.send(receiptResult(root, "acp-host:8"))
				if tc.noteLate {
					host.send(ackNote("DECLINED: the turn is compacting"))
				}
				host.send(plainResult(steer))
			} else {
				host.send(plainResult(steer))
				host.send(receiptResult(root, "acp-host:8"))
			}
			err := steerOutcome(t, outcome)
			var refusal interface{ ChatRefusal() bool }
			if !errors.Is(err, errSteerDeclined) || !errors.As(err, &refusal) || !refusal.ChatRefusal() {
				t.Fatalf("Steer = %v, want the agent's refusal", err)
			}
			done := completionsOf(t, conv, turn)
			if len(done) != 1 || done[0].TurnState != domain.TurnStateCompleted || done[0].ProviderEventID != "acp-host:8" {
				t.Fatalf("turn completions = %+v, want one completed with the host's receipt", done)
			}
		})
	}
}

// The running prompt failing fails the turn, even though the `/steer` command
// took and answered fine after it.
func TestACPSteerPromptAcknowledgedSteerKeepsTheTurnsFailure(t *testing.T) {
	conv, host := startScriptedAckConversation(t, "")
	_ = nextEvent(t, conv.Events()) // controller.ready
	turn, root := startScriptedTurn(t, conv, host)
	steer, outcome := steerAsync(t, conv, host, turn)

	host.send(ackNote("ACK: queued for the active turn"))
	host.send(`{"jsonrpc":"2.0","id":` + root + `,"error":{"code":-32603,"message":"model request failed","data":{"` +
		persistenthost.ACPEventIDMetaKey + `":"acp-host:14"}}}`)
	host.send(plainResult(steer))
	if err := steerOutcome(t, outcome); err != nil {
		t.Fatalf("Steer = %v, want delivered", err)
	}
	done := completionsOf(t, conv, turn)
	if len(done) != 1 || done[0].TurnState != domain.TurnStateFailed || done[0].ProviderEventID != "acp-host:14" {
		t.Fatalf("turn completions = %+v, want one failed with the host's receipt", done)
	}
}

// A `/steer` answer that never comes does not hold a turn the host has ended.
func TestACPSteerPromptMissingSteerAnswerDoesNotHoldTheTurn(t *testing.T) {
	conv, host := startScriptedAckConversation(t, "")
	conv.stragglerWait = 100 * time.Millisecond
	_ = nextEvent(t, conv.Events()) // controller.ready
	turn, root := startScriptedTurn(t, conv, host)
	steer, outcome := steerAsync(t, conv, host, turn)

	host.send(ackNote("ACK: queued for the active turn"))
	if err := steerOutcome(t, outcome); err != nil {
		t.Fatalf("Steer = %v, want delivered", err)
	}
	host.send(receiptResult(root, "acp-host:15"))
	done := completionsOf(t, conv, turn)
	if len(done) != 1 || done[0].TurnState != domain.TurnStateCompleted || done[0].ProviderEventID != "acp-host:15" {
		t.Fatalf("turn completions = %+v, want one completed with the host's receipt", done)
	}
	// The answer arriving after all does not complete the turn again.
	host.send(plainResult(steer))
	if again := turnCompletions(t, conv.Events(), turn, 200*time.Millisecond); len(again) != 0 {
		t.Fatalf("a late steer answer completed the turn again: %+v", again)
	}
}

// After a daemon restart mid-turn, an acknowledging agent can be steered too;
// the turn ends with the host's result for the prompt it adopted.
func TestACPSteerPromptAcknowledgedSteerOfAnAdoptedTurn(t *testing.T) {
	conv, host := startScriptedAckConversation(t, "durable-turn")
	_ = nextEvent(t, conv.Events()) // controller.ready
	steer, outcome := steerAsync(t, conv, host, "durable-turn")
	host.send(ackNote("ACK: queued for the active turn"))
	host.send(plainResult(steer))
	if err := steerOutcome(t, outcome); err != nil {
		t.Fatalf("Steer = %v, want delivered", err)
	}
	host.send(`{"jsonrpc":"2.0","method":"_ao/persistent_prompt_result","params":{"eventId":"acp-host:9","result":{"stopReason":"end_turn"}}}`)
	done := completionsOf(t, conv, "durable-turn")
	if len(done) != 1 || done[0].TurnState != domain.TurnStateCompleted || done[0].ProviderEventID != "acp-host:9" {
		t.Fatalf("turn completions = %+v, want one completed with the host's receipt", done)
	}
}
