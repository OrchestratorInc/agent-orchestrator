package acp

import (
	"context"
	"errors"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Removing the provider fence must make this fail: a cancelled ACP result
// alone is not proof that the native runtime released its root turn.
func TestACPInterruptedTurnWaitsForNativeSettlement(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "released", true: "unconfirmed"}[fail], func(t *testing.T) {
			entered := make(chan struct{})
			release := make(chan struct{})
			conv := &conversation{
				sessionID: "native-session", activeTurn: "turn-1",
				events:     make(chan ports.ChatEvent, 8),
				legacyWire: &legacyACPTransport{},
				afterInterrupt: func(ctx context.Context, _ AgentRequest, id string) error {
					if id != "native-session" {
						t.Errorf("settlement session = %q", id)
					}
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
						return ctx.Err()
					}
					if fail {
						return errors.New("native root still closing")
					}
					return nil
				},
			}
			done := make(chan struct{})
			go func() {
				conv.finishPrompt("turn-1", acpsdk.PromptResponse{
					StopReason: acpsdk.StopReasonCancelled,
					Meta:       map[string]any{persistenthost.ACPEventIDMetaKey: "terminal-1"},
				}, nil)
				close(done)
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("provider settlement not invoked")
			}
			if _, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "too early"}); err == nil {
				t.Fatal("next prompt admitted before native settlement")
			}
			select {
			case event := <-conv.Events():
				t.Fatalf("terminal/ready emitted before native settlement: %+v", event)
			default:
			}
			close(release)
			<-done
			if fail {
				event := nextEvent(t, conv.Events())
				if event.ControllerState != ports.ChatControllerStopped || !errors.Is(event.Err, ports.ErrChatRecoveryInconclusive) {
					t.Fatalf("unconfirmed native stop = %+v", event)
				}
				if conv.terminalEventID != "" || conv.activeTurn != "turn-1" {
					t.Fatal("unconfirmed stop released the turn or allowed terminal ACK")
				}
				if _, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "still blocked"}); err == nil {
					t.Fatal("next prompt admitted after failed native settlement")
				}
			} else {
				event := nextEvent(t, conv.Events())
				if event.Kind != ports.ChatEventTurnCompleted || event.TurnState != domain.TurnStateInterrupted || event.ProviderEventID != "terminal-1" {
					t.Fatalf("confirmed stop = %+v", event)
				}
				if event := nextEvent(t, conv.Events()); event.ControllerState != ports.ChatControllerReady {
					t.Fatalf("confirmed native stop did not announce ready: %+v", event)
				}
				if _, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "next"}); err != nil {
					t.Fatalf("next prompt refused after native release: %v", err)
				}
			}
		})
	}
}
