package acp

import (
	"errors"
	"testing"

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

// The acknowledgement says the steer took. The turn settles with the host's
// result, whichever of the two answers the SDK hands over first.
func TestACPSteerPromptAcknowledgedSteerKeepsTheHostResultInEitherOrder(t *testing.T) {
	for _, order := range []struct {
		name          string
		first, second int
	}{
		{"steer answer routed first", 1, 0},
		{"turn result routed first", 0, 1},
	} {
		t.Run(order.name, func(t *testing.T) {
			conv, host := startScriptedAckConversation(t, "")
			answerOrder(conv, order.first, order.second)
			_ = nextEvent(t, conv.Events()) // controller.ready
			turn, root := startScriptedTurn(t, conv, host)
			steer, outcome := steerAsync(t, conv, host, turn)

			host.send(ackNote("ACK: queued for the active turn"))
			host.send(plainResult(steer))
			host.send(receiptResult(root, "acp-host:7"))
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
		// steerLast makes the steer prompt the last to answer, so its answer
		// carries the host's receipt.
		steerLast bool
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
			if tc.steerLast {
				host.send(plainResult(root))
				if tc.noteLate {
					host.send(ackNote("DECLINED: the turn is compacting"))
				}
				host.send(receiptResult(steer, "acp-host:8"))
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
