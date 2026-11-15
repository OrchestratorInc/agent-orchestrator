package claudeacp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	fakeClaudeTurnEnv    = "AO_TEST_FAKE_CLAUDE_TURN"
	fakeClaudeLogEnv     = "AO_TEST_FAKE_CLAUDE_LOG"
	requireACPRuntimeEnv = "AO_TEST_REQUIRE_ACP_RUNTIME"

	fakeClaudeQuotedAnswer = "quoted-answer"
	fakeClaudeNotLoggedIn  = "not-logged-in"

	// An answer that quotes Claude Code's login advice, like one explaining a
	// login bug. Unpatched, claude-agent-acp 0.70 reports any result containing
	// "Please run /login" as a refused login.
	quotedLoginAnswer = "Those turns were not real logouts. Each answer quoted the CLI's advice, " +
		"\"Not logged in · Please run /login\", while explaining the bug."
	notLoggedInText = "Not logged in · Please run /login"
)

// The packaged claude-agent-acp, driven by a fake Claude Code that replays what
// the real CLI prints. It fails when an adapter upgrade or a packaging change
// alters how a refused login reaches AO: an answer that quotes the login advice
// must complete without the adapter reporting a refusal, and a real refusal must
// still fail the turn and ask for a login.
func TestPackagedAdapterTellsQuotedLoginAdviceFromARefusal(t *testing.T) {
	packagedACPRuntime(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, turn string
		want       domain.TurnState
		answer     string
		refused    bool
	}{
		{"answer quoting the login advice", fakeClaudeQuotedAnswer, domain.TurnStateCompleted, quotedLoginAnswer, false},
		{"not logged in", fakeClaudeNotLoggedIn, domain.TurnStateFailed, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cliLog := filepath.Join(t.TempDir(), "claude-cli.log")
			t.Setenv(fakeClaudeTurnEnv, tc.turn)
			t.Setenv(fakeClaudeLogEnv, cliLog)
			// Keep the adapter off the developer's own Claude settings.
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			// Avoid model discovery, which would run the fake as a plain CLI.
			t.Setenv("CLAUDE_MODEL_CONFIG", `{"availableModels":["fixture-model"]}`)
			t.Cleanup(func() {
				if t.Failed() {
					if data, err := os.ReadFile(cliLog); err == nil {
						t.Logf("fake Claude Code log:\n%s", data)
					}
				}
			})

			var authRejected atomic.Int32
			warned := &warnings{}
			driver := New(quotaFixtureClaudePlugin{binary: executable}, slog.New(warned), func() { authRejected.Add(1) })
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			dataDir := t.TempDir()
			const sessionID = "claude-login-refusal-fixture"
			t.Cleanup(func() { _ = persistenthost.Shutdown(context.Background(), dataDir, sessionID) })
			conversation, err := driver.Start(ctx, ports.ChatStartConfig{
				SessionID: sessionID, DataDir: dataDir, WorkspacePath: t.TempDir(),
			})
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer conversation.(ports.ChatProviderTerminator).Terminate()
			ref, err := conversation.SendTurn(ctx, ports.ChatUserMessage{
				Text: "Were those real logouts?", ClientMessageID: "prompt-1", Origin: domain.MessageOriginHuman,
			})
			if err != nil {
				t.Fatalf("SendTurn: %v", err)
			}
			if err := conversation.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
				t.Fatal(err)
			}

			var shown strings.Builder
			var completed *ports.ChatEvent
			for completed == nil {
				select {
				case event, ok := <-conversation.Events():
					if !ok {
						t.Fatal("driver closed before the turn completed")
					}
					switch event.Kind {
					case ports.ChatEventMessageDelta:
						shown.WriteString(event.Delta)
					case ports.ChatEventTurnCompleted:
						if event.ProviderTurnID == ref.ProviderTurnID {
							completed = &event
						}
					}
				case <-ctx.Done():
					t.Fatalf("turn never completed: %v", ctx.Err())
				}
			}
			if err := conversation.(ports.ChatProviderEventAcknowledger).AcknowledgeProviderEvent(ctx, completed.ProviderEventID); err != nil {
				t.Fatal(err)
			}

			if completed.TurnState != tc.want {
				t.Fatalf("turn = %q (err %v), want %q", completed.TurnState, completed.Err, tc.want)
			}
			if refused := errors.Is(completed.Err, ports.ErrChatAuthRequired); refused != tc.refused {
				t.Fatalf("turn error = %v, want refused login = %v", completed.Err, tc.refused)
			}
			if got := strings.TrimSpace(shown.String()); got != tc.answer {
				t.Fatalf("chat showed %q, want %q", got, tc.answer)
			}
			wantRejected := int32(0)
			if tc.refused {
				wantRejected = 1
			}
			if got := authRejected.Load(); got != wantRejected {
				t.Fatalf("login invalidated %d times, want %d", got, wantRejected)
			}
			// The chat driver's own check (acp/conversation.go) logs this when it has to
			// overrule the adapter. The packaged adapter carries AO's login result patch,
			// so it must not report the quoted answer as a refusal in the first place.
			if warned.logged("ignored a refused login") {
				t.Fatal("the packaged adapter reported the quoted answer as a refused login; is patchClaudeLoginResultError applied?")
			}
		})
	}
}

// warnings records the driver's warnings.
type warnings struct {
	mu       sync.Mutex
	messages []string
}

func (w *warnings) Enabled(_ context.Context, level slog.Level) bool { return level >= slog.LevelWarn }

func (w *warnings) Handle(_ context.Context, record slog.Record) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.messages = append(w.messages, record.Message)
	return nil
}

func (w *warnings) WithAttrs([]slog.Attr) slog.Handler { return w }
func (w *warnings) WithGroup(string) slog.Handler      { return w }

func (w *warnings) logged(text string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, message := range w.messages {
		if strings.Contains(message, text) {
			return true
		}
	}
	return false
}

// packagedACPRuntime points the driver at the runtime `npm run build:acp-runtime`
// builds. CI sets AO_TEST_REQUIRE_ACP_RUNTIME so a missing runtime fails
// instead of skipping.
func packagedACPRuntime(t *testing.T) {
	t.Helper()
	dir := strings.TrimSpace(os.Getenv("AO_ACP_RUNTIME_DIR"))
	if dir == "" {
		dir = filepath.Join("..", "..", "..", "..", "..", "frontend", "resources", "acp-runtime")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AO_ACP_RUNTIME_DIR", dir)
	// A developer's own adapter would replace the packaged one this test checks.
	t.Setenv("AO_CLAUDE_ACP_COMMAND", "")
	if _, err := resolveRuntime(context.Background()); err != nil {
		if os.Getenv(requireACPRuntimeEnv) == "1" {
			t.Fatalf("packaged ACP runtime: %v", err)
		}
		t.Skipf("no packaged ACP runtime (run npm run build:acp-runtime in frontend/): %v", err)
	}
}

// serveFakeClaudeCode stands in for the Claude Code executable the adapter
// starts. It speaks the SDK's stream-json protocol and answers every prompt
// with the records the real CLI prints for the given turn. The not-logged-in
// records were captured from Claude Code 2.1.323 run with no credentials.
func serveFakeClaudeCode(turn string) error {
	logFile := os.Getenv(fakeClaudeLogEnv)
	logLine := func(format string, args ...any) {
		if logFile == "" {
			return
		}
		f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return
		}
		defer f.Close()
		_, _ = fmt.Fprintf(f, format+"\n", args...)
	}
	logLine("args: %q", os.Args[1:])

	sessionID := uuid.NewString()
	for i, arg := range os.Args {
		if value, ok := strings.CutPrefix(arg, "--session-id="); ok {
			sessionID = value
		}
		if (arg == "--session-id" || arg == "--resume") && i+1 < len(os.Args) {
			sessionID = os.Args[i+1]
		}
	}
	out := json.NewEncoder(os.Stdout)
	write := func(record map[string]any) error {
		if _, ok := record["session_id"]; !ok && record["type"] != "control_response" {
			record["session_id"] = sessionID
		}
		if _, ok := record["uuid"]; !ok && record["type"] != "control_response" {
			record["uuid"] = uuid.NewString()
		}
		return out.Encode(record)
	}

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for in.Scan() {
		logLine("stdin: %s", in.Bytes())
		var message struct {
			Type      string         `json:"type"`
			RequestID string         `json:"request_id"`
			Request   map[string]any `json:"request"`
		}
		if err := json.Unmarshal(in.Bytes(), &message); err != nil {
			return err
		}
		switch message.Type {
		case "control_request":
			response := map[string]any{}
			if message.Request["subtype"] == "initialize" {
				response = map[string]any{
					"commands": []any{}, "agents": []any{}, "account": map[string]any{},
					"output_style": "default", "available_output_styles": []any{"default"},
					"models": []any{map[string]any{
						"value": "fixture-model", "displayName": "Fixture", "description": "Fixture model",
					}},
				}
			}
			if err := write(map[string]any{"type": "control_response", "response": map[string]any{
				"subtype": "success", "request_id": message.RequestID, "response": response,
			}}); err != nil {
				return err
			}
		case "user":
			for _, record := range fakeClaudeTurn(turn) {
				if err := write(record); err != nil {
					return err
				}
			}
		}
	}
	return in.Err()
}

func fakeClaudeTurn(turn string) []map[string]any {
	records := []map[string]any{{
		"type": "system", "subtype": "init", "cwd": ".", "model": "fixture-model",
		"permissionMode": "default", "apiKeySource": "none", "claude_code_version": "2.1.323",
		"output_style": "default", "tools": []any{}, "mcp_servers": []any{}, "slash_commands": []any{},
		"agents": []any{}, "skills": []any{}, "plugins": []any{},
	}}
	zeroUsage := map[string]any{"input_tokens": 0, "output_tokens": 0, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0}
	switch turn {
	case fakeClaudeNotLoggedIn:
		return append(records,
			map[string]any{
				"type": "assistant", "parent_tool_use_id": nil, "error": "authentication_failed", "is_api_error_message": true,
				"message": map[string]any{
					"id": uuid.NewString(), "type": "message", "role": "assistant", "model": "<synthetic>",
					"stop_reason": "stop_sequence", "stop_sequence": "", "usage": zeroUsage,
					"content": []any{map[string]any{"type": "text", "text": notLoggedInText}},
				},
			},
			map[string]any{
				"type": "result", "subtype": "success", "is_error": true, "result": notLoggedInText,
				"stop_reason": "stop_sequence", "terminal_reason": "api_error", "api_error_status": nil,
				"duration_ms": 1, "duration_api_ms": 0, "num_turns": 1, "total_cost_usd": 0,
				"usage": zeroUsage, "modelUsage": map[string]any{}, "permission_denials": []any{},
			},
		)
	case fakeClaudeQuotedAnswer:
		const messageID = "msg_fixture_quoted_answer"
		usage := map[string]any{"input_tokens": 12, "output_tokens": 30, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0}
		half := strings.Index(quotedLoginAnswer, "\"Not")
		stream := func(event map[string]any) map[string]any {
			return map[string]any{"type": "stream_event", "parent_tool_use_id": nil, "event": event}
		}
		return append(records,
			stream(map[string]any{"type": "message_start", "message": map[string]any{
				"id": messageID, "type": "message", "role": "assistant", "model": "fixture-model",
				"content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": usage,
			}}),
			stream(map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}}),
			stream(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": quotedLoginAnswer[:half]}}),
			stream(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": quotedLoginAnswer[half:]}}),
			stream(map[string]any{"type": "content_block_stop", "index": 0}),
			map[string]any{
				"type": "assistant", "parent_tool_use_id": nil,
				"message": map[string]any{
					"id": messageID, "type": "message", "role": "assistant", "model": "fixture-model",
					"stop_reason": nil, "stop_sequence": nil, "usage": usage,
					"content": []any{map[string]any{"type": "text", "text": quotedLoginAnswer}},
				},
			},
			stream(map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 30}}),
			stream(map[string]any{"type": "message_stop"}),
			map[string]any{
				"type": "result", "subtype": "success", "is_error": false, "result": quotedLoginAnswer,
				"stop_reason": "end_turn", "terminal_reason": "completed",
				"duration_ms": 1, "duration_api_ms": 1, "num_turns": 1, "total_cost_usd": 0, "usage": usage,
				"modelUsage": map[string]any{"fixture-model": map[string]any{
					"inputTokens": 12, "outputTokens": 30, "cacheReadInputTokens": 0, "cacheCreationInputTokens": 0,
					"webSearchRequests": 0, "costUSD": 0, "contextWindow": 200000, "maxOutputTokens": 32000,
				}},
				"permission_denials": []any{},
			},
		)
	}
	return records
}
