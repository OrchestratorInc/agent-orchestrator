package codebuddyacp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// This executable is explicitly synthetic. These tests qualify AO's binding and
// detached transport boundaries; they do not assert real CodeBuddy conformance.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		version := os.Getenv("AO_TEST_CODEBUDDY_VERSION")
		if version == "" {
			version = candidateVersion
		}
		_, _ = fmt.Println(version)
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Args[1] == "--acp" {
		runSyntheticCodeBuddy()
		os.Exit(0)
	}
	if len(os.Args) >= 8 && os.Args[1] == "chat-host" {
		if os.Args[5] != string(persistenthost.ProtocolACP) || os.Args[7] != "--" {
			os.Exit(2)
		}
		err := persistenthost.Run(context.Background(), persistenthost.Config{
			SessionID: os.Args[2], DataDir: os.Args[3], Workdir: os.Args[4], Env: os.Environ(),
			Argv: os.Args[8:], Protocol: persistenthost.ProtocolACP, OwnershipFingerprint: os.Args[6],
		})
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

const syntheticSession = "cb-synthetic-session-20261019"

func runSyntheticCodeBuddy() {
	trace := func(method, detail string) {
		f, err := os.OpenFile(os.Getenv("AO_TEST_CODEBUDDY_TRACE"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if err != nil {
			return
		}
		_ = json.NewEncoder(f).Encode(map[string]any{"method": method, "detail": detail, "pid": os.Getpid()})
		_ = f.Close()
	}
	prompt := ""
	for i, arg := range os.Args {
		if arg == "--append-system-prompt" && i+1 < len(os.Args) {
			prompt = os.Args[i+1]
		}
	}
	trace("launch", prompt)
	out := json.NewEncoder(os.Stdout)
	update := func(kind, text, id string) {
		_ = out.Encode(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
			"sessionId": syntheticSession, "update": map[string]any{"sessionUpdate": kind, "messageId": id, "content": map[string]string{"type": "text", "text": text}},
		}})
	}
	var parkedPrompt json.RawMessage
	scan := bufio.NewScanner(os.Stdin)
	for scan.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				SessionID string `json:"sessionId"`
				Prompt    []struct {
					Text string `json:"text"`
				} `json:"prompt"`
			} `json:"params"`
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(scan.Bytes(), &req) != nil {
			continue
		}
		trace(req.Method, req.Params.SessionID)
		if req.Method == "" && string(req.ID) == `"permission-1"` && len(parkedPrompt) > 0 {
			trace("permission-response", string(req.Result))
			update("agent_message_chunk", "synthetic decision settled", "00000000-0000-4000-8000-000000000003")
			_ = out.Encode(map[string]any{"jsonrpc": "2.0", "id": parkedPrompt, "result": map[string]string{"stopReason": "end_turn"}})
			parkedPrompt = nil
			continue
		}
		if req.Method == "session/cancel" && len(parkedPrompt) > 0 {
			_ = out.Encode(map[string]any{"jsonrpc": "2.0", "id": parkedPrompt, "result": map[string]string{"stopReason": "cancelled"}})
			parkedPrompt = nil
			continue
		}
		if len(req.ID) == 0 {
			continue
		}
		var result any = map[string]any{}
		var rpcErr any
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": 1, "authMethods": []any{}, "agentCapabilities": map[string]any{
				"loadSession": true, "promptCapabilities": map[string]bool{"image": true, "embeddedContext": true},
			}, "_meta": map[string]any{"steering": map[string]bool{"supported": true}}}
		case "session/new", "session/load":
			if os.Getenv("AO_TEST_CODEBUDDY_AUTH") == "reject" {
				rpcErr = map[string]any{"code": -32000, "message": "Authentication required", "data": map[string]string{"category": "auth"}}
			} else if req.Method == "session/load" && req.Params.SessionID != syntheticSession {
				rpcErr = map[string]any{"code": -32002, "message": "Resource not found"}
			} else {
				result = map[string]any{"configOptions": []any{map[string]any{
					"id": "model", "name": "Model", "type": "select", "currentValue": "native-default",
					"options": []any{map[string]string{"value": "native-default", "name": "Native default"}, map[string]string{"value": "unqualified", "name": "Unqualified"}},
				}}}
				if req.Method == "session/new" {
					result.(map[string]any)["sessionId"] = syntheticSession
				} else {
					update("user_message_chunk", "ordinary task", "00000000-0000-4000-8000-000000000001")
					update("agent_message_chunk", "synthetic answer", "00000000-0000-4000-8000-000000000002")
				}
			}
		case "session/prompt":
			text := ""
			for _, part := range req.Params.Prompt {
				text += part.Text
			}
			if strings.Contains(text, "permission") {
				parkedPrompt = append(json.RawMessage(nil), req.ID...)
				_ = out.Encode(map[string]any{"jsonrpc": "2.0", "id": "permission-1", "method": "session/request_permission", "params": map[string]any{
					"sessionId": syntheticSession, "toolCall": map[string]any{"toolCallId": "synthetic-tool", "title": "Synthetic shell approval", "kind": "execute"},
					"options": []any{map[string]string{"optionId": "allow", "name": "Allow once", "kind": "allow_once"}, map[string]string{"optionId": "reject", "name": "Reject once", "kind": "reject_once"}},
				}})
				continue
			}
			if strings.Contains(text, "hold") {
				parkedPrompt = append(json.RawMessage(nil), req.ID...)
				update("agent_message_chunk", "synthetic waiting", "00000000-0000-4000-8000-000000000004")
				continue
			}
			update("agent_message_chunk", "synthetic answer", "00000000-0000-4000-8000-000000000002")
			result = map[string]string{"stopReason": "end_turn"}
		}
		response := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		if rpcErr != nil {
			response["error"] = rpcErr
		} else {
			response["result"] = result
		}
		if out.Encode(response) != nil {
			return
		}
	}
}

type lifecyclePlugin struct {
	binary     string
	historyErr error
}

func (p *lifecyclePlugin) ResolveBinary(context.Context) (string, error) { return p.binary, nil }
func (*lifecyclePlugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	return ports.AgentAuthStatusConfigured, nil
}
func (p *lifecyclePlugin) ValidateChatHistory(context.Context, ports.ChatResumeConfig) error {
	return p.historyErr
}

type lifecycleFixture struct {
	ctx     context.Context
	binding ports.ChatDriver
	plugin  *lifecyclePlugin
	cfg     ports.ChatStartConfig
	trace   string
}

func newLifecycleFixture(t *testing.T) lifecycleFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, workspace := t.TempDir(), t.TempDir()
	profile := filepath.Join(data, "agents", "codebuddy", "chat", "codebuddy-candidate-test")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, ".ao-managed"), []byte(profileMarker), 0o600); err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(data, "synthetic-calls.jsonl")
	plugin := &lifecyclePlugin{binary: binary}
	fixture := lifecycleFixture{ctx: ctx, binding: NewCandidate(plugin, nil), plugin: plugin, trace: trace,
		cfg: ports.ChatStartConfig{SessionID: "codebuddy-candidate-test", DataDir: data, WorkspacePath: workspace, SystemPrompt: "PRIVATE-FIRST-SYNTHETIC", Permissions: ports.PermissionModeDefault,
			Env: map[string]string{"AO_TEST_CODEBUDDY_TRACE": trace, "CODEBUDDY_CONFIG_DIR": profile}},
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = persistenthost.Shutdown(cleanupCtx, data, string(fixture.cfg.SessionID))
	})
	return fixture
}
func (f lifecycleFixture) resume(id string) ports.ChatResumeConfig {
	return ports.ChatResumeConfig{SessionID: f.cfg.SessionID, DataDir: f.cfg.DataDir, WorkspacePath: f.cfg.WorkspacePath, Env: f.cfg.Env,
		ProviderConversationID: id, Permissions: ports.PermissionModeDefault, FreshIfMissing: true, HasPriorTurns: true, SystemPrompt: "PRIVATE-RESTORE-SYNTHETIC"}
}
func traceCalls(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var calls []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var call map[string]any
		if err := json.Unmarshal([]byte(line), &call); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, call)
	}
	return calls
}
func callCount(calls []map[string]any, method string) int {
	n := 0
	for _, c := range calls {
		if c["method"] == method {
			n++
		}
	}
	return n
}

func TestCandidateSyntheticLifecycleAndStrictRestore(t *testing.T) {
	f := newLifecycleFixture(t)
	conv, err := f.binding.Start(f.ctx, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if conv.ProviderConversationID() != syntheticSession {
		t.Fatal("native identity lost")
	}
	for _, capability := range []ports.ChatCapability{ports.ChatCapabilityImages, ports.ChatCapabilityEmbeddedContext, ports.ChatCapabilitySteer} {
		if conv.Capabilities().Has(capability) {
			t.Fatalf("unqualified capability enabled: %s", capability)
		}
	}
	if _, err := conv.(ports.ChatConfigOptionController).SetConfigOption(f.ctx, "model", ports.ChatConfigOptionValue{Select: "unqualified"}); !errors.Is(err, ports.ErrChatConfigOptionInvalid) {
		t.Fatalf("picker bypassed settings guard: %v", err)
	}
	ref, err := conv.SendTurn(f.ctx, ports.ChatUserMessage{Text: "ordinary task"})
	if err != nil {
		t.Fatal(err)
	}
	if err := conv.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatal(err)
	}
	completed := false
	observedAnswer := false
	for !completed {
		select {
		case e, ok := <-conv.Events():
			if !ok {
				t.Fatal("closed before completion")
			}
			if strings.Contains(e.Text, "synthetic answer") {
				observedAnswer = true
			}
			if e.Kind == ports.ChatEventTurnCompleted {
				if e.TurnState != domain.TurnStateCompleted || e.ProviderEventID == "" || !observedAnswer {
					t.Fatalf("turn did not settle with answer and durable receipt: %#v", e)
				}
				if err := conv.(ports.ChatProviderEventAcknowledger).AcknowledgeProviderEvent(f.ctx, e.ProviderEventID); err != nil {
					t.Fatal(err)
				}
				completed = true
			}
		case <-f.ctx.Done():
			t.Fatal(f.ctx.Err())
		}
	}
	if err := conv.(ports.ChatProviderTerminator).Terminate(); err != nil {
		t.Fatal(err)
	}
	cfg := f.resume(syntheticSession)
	conv, err = f.binding.Resume(f.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if conv.ProviderConversationID() != syntheticSession || conv.(ports.ChatLiveReconnector).ReconnectedLive() {
		t.Fatal("replacement did not load exact history")
	}
	history, err := conv.(ports.ChatHistoryReader).ReadHistory(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	users, answers := 0, 0
	for _, e := range history {
		if e.Kind == ports.ChatEventUserMessageCompleted {
			users++
		}
		if e.Kind == ports.ChatEventMessageCompleted {
			answers++
		}
		if strings.Contains(e.Text, "PRIVATE-") {
			t.Fatal("private instructions became history")
		}
	}
	if users != 1 || answers != 1 {
		t.Fatalf("replay users=%d answers=%d", users, answers)
	}
	if err := conv.(ports.ChatProviderTerminator).Terminate(); err != nil {
		t.Fatal(err)
	}
	_, err = f.binding.Resume(f.ctx, f.resume("cb-missing"))
	if !errors.Is(err, ports.ErrChatResumeFailed) {
		t.Fatalf("missing history was not refused: %v", err)
	}
	calls := traceCalls(t, f.trace)
	if callCount(calls, "session/new") != 1 || callCount(calls, "session/set_config_option") != 0 {
		t.Fatalf("fresh fallback or unsafe setter: %#v", calls)
	}
	starts := []string{}
	for _, c := range calls {
		if c["method"] == "launch" {
			starts = append(starts, c["detail"].(string))
		}
	}
	if len(starts) != 3 || starts[0] != "PRIVATE-FIRST-SYNTHETIC" || starts[1] != "PRIVATE-RESTORE-SYNTHETIC" {
		t.Fatalf("launch context not refreshed: %q", starts)
	}
}

func TestCandidateSyntheticFirstTurnLiveAdoptionBypassesReplacementHistory(t *testing.T) {
	f := newLifecycleFixture(t)
	conv, err := f.binding.Start(f.ctx, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	f.plugin.historyErr = errors.New("no completed assistant history yet")
	if err := conv.Close(); err != nil {
		t.Fatal(err)
	}
	conv, err = f.binding.Resume(f.ctx, f.resume(syntheticSession))
	if err != nil {
		t.Fatal(err)
	}
	if !conv.(ports.ChatLiveReconnector).ReconnectedLive() {
		t.Fatal("live host replaced")
	}
	if err := conv.Close(); err != nil {
		t.Fatal(err)
	}
	conv, err = f.binding.(ports.ChatDriverReconnector).Reconnect(f.ctx, f.resume(syntheticSession))
	if err != nil {
		t.Fatal(err)
	}
	if !conv.(ports.ChatLiveReconnector).ReconnectedLive() {
		t.Fatal("attach-only launched replacement")
	}
	if err := conv.(ports.ChatProviderTerminator).Terminate(); err != nil {
		t.Fatal(err)
	}
	calls := traceCalls(t, f.trace)
	if callCount(calls, "launch") != 1 || callCount(calls, "initialize") != 1 || callCount(calls, "session/new") != 1 || callCount(calls, "session/load") != 0 {
		t.Fatalf("adoption repeated native setup: %#v", calls)
	}
	before := len(calls)
	_, err = f.binding.Resume(f.ctx, f.resume(syntheticSession))
	if !errors.Is(err, ports.ErrChatResumeFailed) {
		t.Fatalf("replacement skipped history preflight: %v", err)
	}
	if len(traceCalls(t, f.trace)) != before {
		t.Fatal("failed history preflight launched native")
	}
	_, err = f.binding.(ports.ChatDriverReconnector).Reconnect(f.ctx, f.resume(syntheticSession))
	if !errors.Is(err, ports.ErrChatHostNotRunning) {
		t.Fatalf("missing live host started fresh: %v", err)
	}
}

func TestCandidateSyntheticAuthAndDistributionFailures(t *testing.T) {
	t.Run("auth", func(t *testing.T) {
		f := newLifecycleFixture(t)
		f.cfg.Env["AO_TEST_CODEBUDDY_AUTH"] = "reject"
		_, err := f.binding.Start(f.ctx, f.cfg)
		if !errors.Is(err, ports.ErrChatAuthRequired) {
			t.Fatalf("native auth failure lost: %v", err)
		}
	})
	t.Run("distribution", func(t *testing.T) {
		f := newLifecycleFixture(t)
		t.Setenv("AO_TEST_CODEBUDDY_VERSION", "2.165.0")
		_, err := f.binding.Start(f.ctx, f.cfg)
		if !errors.Is(err, ports.ErrChatDriverIncompatible) {
			t.Fatalf("unqualified distribution started: %v", err)
		}
		if _, err := os.Stat(f.trace); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("provider launched before version admission: %v", err)
		}
	})
}

func TestCandidateRefusesUnownedNativeProfile(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint(missing), func(t *testing.T) {
			f := newLifecycleFixture(t)
			if missing {
				delete(f.cfg.Env, "CODEBUDDY_CONFIG_DIR")
			} else {
				f.cfg.Env["CODEBUDDY_CONFIG_DIR"] = t.TempDir()
			}
			if _, err := f.binding.Start(f.ctx, f.cfg); !errors.Is(err, ports.ErrChatDriverUnavailable) {
				t.Fatalf("unowned native profile admitted: %v", err)
			}
			if _, err := os.Stat(f.trace); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unowned profile reached provider: %v", err)
			}
		})
	}
}

func TestCandidateSyntheticPermissionAndInterruptPorts(t *testing.T) {
	for _, choice := range []string{"allow", "reject", "cancel", "interrupt"} {
		t.Run(choice, func(t *testing.T) {
			f := newLifecycleFixture(t)
			conv, err := f.binding.Start(f.ctx, f.cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer conv.(ports.ChatProviderTerminator).Terminate()
			task := "ordinary permission task"
			if choice == "interrupt" {
				task = "hold until stopped"
			}
			ref, err := conv.SendTurn(f.ctx, ports.ChatUserMessage{Text: task})
			if err != nil {
				t.Fatal(err)
			}
			if err := conv.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
				t.Fatal(err)
			}
			ready, done, terminal := false, false, false
			for !done {
				select {
				case e, ok := <-conv.Events():
					if !ok {
						t.Fatal("closed before settlement")
					}
					if !ready && choice != "interrupt" && e.Kind == ports.ChatEventApprovalRequested {
						if err := conv.ResolveRequest(f.ctx, e.RequestID, ports.ChatDecision{ID: "unoffered"}); !errors.Is(err, ports.ErrChatDecisionNotOffered) {
							t.Fatalf("invented decision accepted: %v", err)
						}
						if choice == "cancel" {
							if err := conv.Interrupt(f.ctx, ref.ProviderTurnID); err != nil {
								t.Fatal(err)
							}
						} else if err := conv.ResolveRequest(f.ctx, e.RequestID, ports.ChatDecision{ID: choice}); err != nil {
							t.Fatal(err)
						}
						ready = true
					}
					if !ready && choice == "interrupt" && strings.Contains(e.Text, "synthetic waiting") {
						if err := conv.Interrupt(f.ctx, ref.ProviderTurnID); err != nil {
							t.Fatal(err)
						}
						ready = true
					}
					if e.Kind == ports.ChatEventTurnCompleted {
						expected := domain.TurnStateCompleted
						if choice == "interrupt" || choice == "cancel" {
							expected = domain.TurnStateInterrupted
						}
						if !ready || e.TurnState != expected || e.ProviderEventID == "" {
							t.Fatalf("unexpected settlement: %#v", e)
						}
						if err := conv.(ports.ChatProviderEventAcknowledger).AcknowledgeProviderEvent(f.ctx, e.ProviderEventID); err != nil {
							t.Fatal(err)
						}
						terminal = true
					}
					if terminal && e.Kind == ports.ChatEventControllerState && e.ControllerState == ports.ChatControllerReady {
						done = true
					}
				case <-f.ctx.Done():
					t.Fatal(f.ctx.Err())
				}
			}
			calls := traceCalls(t, f.trace)
			if choice == "interrupt" || choice == "cancel" {
				if callCount(calls, "session/cancel") != 1 {
					t.Fatalf("cancel not sent: %#v", calls)
				}
			} else {
				found := false
				for _, call := range calls {
					if call["method"] == "permission-response" && strings.Contains(call["detail"].(string), `"optionId":"`+choice+`"`) {
						found = true
					}
				}
				if !found {
					t.Fatalf("native decision not preserved: %#v", calls)
				}
			}
			// The cancelled synthetic prompt must release admission immediately.
			next, err := conv.SendTurn(f.ctx, ports.ChatUserMessage{Text: "ordinary continuation"})
			if err != nil {
				t.Fatalf("next prompt not admitted: %v", err)
			}
			if err := conv.(ports.ChatDeferredTurnStarter).StartDeferredTurn(next.ProviderTurnID); err != nil {
				t.Fatal(err)
			}
			settled := false
			for !settled {
				select {
				case e, ok := <-conv.Events():
					if !ok {
						t.Fatal("closed during continuation")
					}
					if e.Kind == ports.ChatEventTurnCompleted {
						if e.TurnState != domain.TurnStateCompleted || e.ProviderEventID == "" {
							t.Fatalf("continuation did not complete: %#v", e)
						}
						if err := conv.(ports.ChatProviderEventAcknowledger).AcknowledgeProviderEvent(f.ctx, e.ProviderEventID); err != nil {
							t.Fatal(err)
						}
						settled = true
					}
				case <-f.ctx.Done():
					t.Fatal(f.ctx.Err())
				}
			}
		})
	}
}
