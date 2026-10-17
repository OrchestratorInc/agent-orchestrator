package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
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

// The replay reader must remain free to receive the native settlement RPC's
// response. Duplicate replay notifications must not issue a second stop/commit.
func TestPersistentACPInterruptedReplayCanReadSettlementResponse(t *testing.T) {
	daemon, host := net.Pipe()
	t.Cleanup(func() { _ = host.Close() })
	conv := newConversation(&process{
		stdin: daemon, stdout: daemon, stop: daemon.Close,
		terminate: func() error { return nil },
	}, slog.New(slog.DiscardHandler), "", nil, nil)
	t.Cleanup(func() { _ = conv.Close() })
	conv.mu.Lock()
	conv.sessionID = "native-session"
	conv.activeTurn = "restored-turn"
	conv.afterInterrupt = func(ctx context.Context, request AgentRequest, id string) error {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		_, err := request(ctx, "mcode/session/delegation/stop", map[string]string{"sessionId": id})
		return err
	}
	conv.mu.Unlock()
	server := make(chan error, 1)
	go func() {
		_ = host.SetDeadline(time.Now().Add(5 * time.Second))
		notification := `{"jsonrpc":"2.0","method":"_ao/persistent_prompt_result","params":{"eventId":"replayed-terminal","result":{"stopReason":"cancelled"}}}`
		for range 2 {
			if _, err := fmt.Fprintln(host, notification); err != nil {
				server <- err
				return
			}
		}
		reader := bufio.NewReader(host)
		frame, err := reader.ReadBytes('\n')
		if err != nil {
			server <- err
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				SessionID string `json:"sessionId"`
			} `json:"params"`
		}
		if err := json.Unmarshal(frame, &request); err != nil || request.Method != "mcode/session/delegation/stop" || request.Params.SessionID != "native-session" {
			server <- fmt.Errorf("unexpected settlement request: %s (%v)", frame, err)
			return
		}
		if _, err := fmt.Fprintf(host, `{"jsonrpc":"2.0","id":%s,"result":{}}`+"\n", request.ID); err != nil {
			server <- err
			return
		}
		frame, err = reader.ReadBytes('\n')
		var ack struct {
			Method string `json:"method"`
			Params struct {
				EventID string `json:"eventId"`
			} `json:"params"`
		}
		if err == nil {
			err = json.Unmarshal(frame, &ack)
		}
		if err == nil && (ack.Method != "_ao/persistent_prompt_ack" || ack.Params.EventID != "replayed-terminal") {
			err = fmt.Errorf("duplicate stop or incorrect terminal ACK: %s", frame)
		}
		server <- err
	}()
	event := nextEvent(t, conv.Events())
	if event.Kind != ports.ChatEventTurnCompleted || event.TurnState != domain.TurnStateInterrupted || event.ProviderEventID != "replayed-terminal" {
		t.Fatalf("replayed interrupted result failed settlement: %+v", event)
	}
	if event := nextEvent(t, conv.Events()); event.ControllerState != ports.ChatControllerReady {
		t.Fatalf("replay did not become ready: %+v", event)
	}
	if err := conv.AcknowledgeProviderEvent(context.Background(), "replayed-terminal"); err != nil {
		t.Fatal(err)
	}
	if err := <-server; err != nil {
		t.Fatal(err)
	}
}
