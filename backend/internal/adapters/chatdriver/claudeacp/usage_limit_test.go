package claudeacp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// claude-agent-acp 0.70 returns terminal quota failures as end_turn plus AIR metadata:
// https://github.com/agentclientprotocol/claude-agent-acp/blob/v0.70.0/docs/session-failure-extension.md
const claudeTimedQuotaResponse = `{"stopReason":"end_turn","_meta":{"jetbrains":{"air":{"version":1,"sessionFailure":{"id":"native-prompt:error","revision":1,"category":"limit","severity":"error","title":"You've hit your session limit · resets 3:31pm (Asia/Calcutta)","actions":[]}}}}}`

const claudeUnknownQuotaResponse = `{"stopReason":"end_turn","_meta":{"jetbrains":{"air":{"version":1,"sessionFailure":{"id":"native-prompt:error","revision":1,"category":"limit","severity":"error","title":"You've hit your individual spend limit · run /usage-credits to ask your admin for a higher limit","actions":[]}}}}}`

func TestClaudeNativeQuotaMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		failure, quota bool
	}{
		{"timed quota", claudeTimedQuotaResponse, true, true},
		{"unknown reset", claudeUnknownQuotaResponse, true, true},
		{"connection failure", `{"stopReason":"end_turn","_meta":{"jetbrains":{"air":{"version":1,"sessionFailure":{"id":"native-prompt:error","revision":1,"category":"connection","severity":"error","title":"The connection to Claude was lost.","actions":["new_session"]}}}}}`, true, false},
		{"assistant quota prose", `{"stopReason":"end_turn","_meta":{"assistantMessage":"You've hit your session limit · resets 3:31pm (Asia/Calcutta)"}}`, false, false},
		{"warning", `{"stopReason":"end_turn","_meta":{"jetbrains":{"air":{"version":1,"sessionFailure":{"id":"native-prompt:error","revision":1,"category":"limit","severity":"warning","title":"Usage limit reached","actions":[]}}}}}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var response acpsdk.PromptResponse
			if err := json.Unmarshal([]byte(tc.response), &response); err != nil {
				t.Fatal(err)
			}
			failure := claudePromptResponseFailure(response)
			if (failure != nil) != tc.failure || errors.Is(failure, ports.ErrChatAuthRequired) {
				t.Fatalf("native failure = %v, want failure=%v without login", failure, tc.failure)
			}
			if failure != nil {
				meta := claudeNestedMap(claudeNestedMap(response.Meta, "jetbrains"), "air")
				if title := claudeNestedMap(meta, "sessionFailure")["title"]; failure.Error() != title {
					t.Fatalf("native title changed: %v, want %s", failure, title)
				}
				failedAt := time.Date(2026, 10, 17, 9, 30, 0, 0, time.UTC)
				if quota := domain.ParseConversationUsageLimit(failure.Error(), &failedAt); (quota != nil) != tc.quota {
					t.Fatalf("native failure classified as quota=%v, want %v: %v", quota, tc.quota, failure)
				}
			}
		})
	}
}

type quotaFixtureClaudePlugin struct{ binary string }

func (p quotaFixtureClaudePlugin) ResolveBinary(context.Context) (string, error) {
	return p.binary, nil
}

func TestClaudeDriverNativeQuotaWireAndRecovery(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		knownReset     bool
	}{
		{"known reset", claudeTimedQuotaResponse, true},
		{"unknown reset", claudeUnknownQuotaResponse, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("AO_CLAUDE_ACP_COMMAND", executable)
			t.Setenv("AO_TEST_CLAUDE_USAGE_LIMIT_RESPONSE", tc.response)
			// Avoid model discovery or any call to the installed Claude executable.
			t.Setenv("CLAUDE_MODEL_CONFIG", `{"availableModels":["fixture-model"]}`)
			var authRejected atomic.Int32
			driver := New(quotaFixtureClaudePlugin{binary: executable}, nil, func() { authRejected.Add(1) })
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			dataDir := t.TempDir()
			t.Cleanup(func() { _ = persistenthost.Shutdown(context.Background(), dataDir, "claude-quota-fixture") })
			conversation, err := driver.Start(ctx, ports.ChatStartConfig{
				SessionID: "claude-quota-fixture", DataDir: dataDir, WorkspacePath: t.TempDir(),
			})
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer conversation.(ports.ChatProviderTerminator).Terminate()
			for attempt := range 2 {
				ref, err := conversation.SendTurn(ctx, ports.ChatUserMessage{
					Text: "finish this task", ClientMessageID: fmt.Sprintf("prompt-%d", attempt), Origin: domain.MessageOriginHuman,
				})
				if err != nil {
					t.Fatalf("SendTurn: %v", err)
				}
				if err := conversation.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
					t.Fatal(err)
				}
				completed := false
				ready := false
				for !ready {
					select {
					case event, ok := <-conversation.Events():
						if !ok {
							t.Fatal("driver closed before turn completion")
						}
						if event.Kind == ports.ChatEventError {
							t.Fatalf("terminal failure emitted an extra error: %+v", event)
						}
						if event.Kind == ports.ChatEventControllerState && event.ControllerState == ports.ChatControllerReady && completed {
							ready = true
						}
						if event.Kind == ports.ChatEventTurnCompleted {
							if completed || event.ProviderTurnID == "" || event.ProviderTurnID != ref.ProviderTurnID || event.ProviderEventID == "" {
								t.Fatalf("lost native turn or durable receipt: %+v", event)
							}
							if attempt == 0 {
								if event.TurnState != domain.TurnStateFailed || event.Err == nil {
									t.Fatalf("quota completed successfully: %+v", event)
								}
								failedAt := time.Date(2026, 10, 17, 9, 30, 0, 0, time.UTC)
								limit := domain.ParseConversationUsageLimit(event.Err.Error(), &failedAt)
								if limit == nil || (limit.ResetsAt != nil) != tc.knownReset {
									t.Fatalf("native quota lost its reset semantics: %+v, %v", limit, event.Err)
								}
								if tc.knownReset && !limit.ResetsAt.Equal(time.Date(2026, 10, 17, 10, 1, 0, 0, time.UTC)) {
									t.Fatalf("reset = %v, want 10:01 UTC", limit.ResetsAt)
								}
							} else if event.TurnState != domain.TurnStateCompleted || event.Err != nil {
								t.Fatalf("follow-up retained quota failure: %+v", event)
							}
							if err := conversation.(ports.ChatProviderEventAcknowledger).AcknowledgeProviderEvent(ctx, event.ProviderEventID); err != nil {
								t.Fatal(err)
							}
							completed = true
						}
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
				}
			}
			if authRejected.Load() != 0 {
				t.Fatal("quota failure invalidated login")
			}
		})
	}
}

// serveClaudeUsageLimitFixture is a child-process ACP peer; it never launches Claude.
func serveClaudeUsageLimitFixture(firstResponse string) error {
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	prompts := 0
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			return err
		}
		if len(request.ID) == 0 {
			continue
		}
		result := `{}`
		switch request.Method {
		case "initialize":
			var params acpsdk.InitializeRequest
			if err := json.Unmarshal(request.Params, &params); err != nil {
				return err
			}
			air := claudeNestedMap(claudeNestedMap(params.ClientCapabilities.Meta, "jetbrains"), "air")
			capabilities, _ := air["capabilities"].([]any)
			if air["version"] != float64(1) || len(capabilities) != 1 || capabilities[0] != "sessionFailure" {
				return errors.New("Claude sessionFailure extension was not negotiated")
			}
			result = `{"protocolVersion":1,"agentCapabilities":{}}`
		case "session/new":
			result = `{"sessionId":"native-claude-session"}`
		case "session/prompt":
			prompts++
			result = `{"stopReason":"end_turn"}`
			if prompts == 1 {
				result = firstResponse
			}
		case "session/close":
		default:
			return fmt.Errorf("unexpected fixture request %q", request.Method)
		}
		if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": json.RawMessage(result)}); err != nil {
			return err
		}
	}
	return scanner.Err()
}
