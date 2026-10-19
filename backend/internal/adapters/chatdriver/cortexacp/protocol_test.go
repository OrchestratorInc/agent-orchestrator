package cortexacp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// This controlled provider exercises the real constructor and detached host in
// CI. It is not a Cortex executable or authenticated/native conformance proof.
func TestMain(m *testing.M) {
	if len(os.Args) >= 8 && os.Args[1] == "chat-host" {
		if os.Args[5] != string(persistenthost.ProtocolACP) || os.Args[7] != "--" {
			os.Exit(2)
		}
		err := persistenthost.Run(context.Background(), persistenthost.Config{
			SessionID: os.Args[2], DataDir: os.Args[3], Workdir: os.Args[4], Env: os.Environ(),
			Protocol: persistenthost.ProtocolACP, OwnershipFingerprint: os.Args[6], Argv: os.Args[8:],
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if len(os.Args) >= 3 && os.Args[1] == "acp" && os.Args[2] == "serve" {
		runProtocolFixture()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

const fixtureOptions = `[{"id":"mode","name":"Mode","type":"select","currentValue":"standard","options":[{"value":"standard","name":"Standard"}]},{"id":"model","name":"Model","type":"select","currentValue":"auto","options":[{"value":"auto","name":"Auto"},{"value":"fixture-model","name":"Fixture"}]}]`

func runProtocolFixture() {
	scanner := bufio.NewScanner(os.Stdin)
	var pending json.RawMessage
	for scanner.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			continue
		}
		trace, err := os.OpenFile(filepath.Join(os.Getenv("HOME"), "protocol-calls"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			os.Exit(2)
		}
		_, _ = trace.Write(append(append([]byte(nil), scanner.Bytes()...), '\n'))
		_ = trace.Close()
		reply := func(result string) {
			fmt.Fprintf(os.Stdout, "{\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":%s}\n", req.ID, result)
		}
		failure := func() {
			fmt.Fprintf(os.Stdout, "{\"jsonrpc\":\"2.0\",\"id\":%s,\"error\":{\"code\":-32002,\"message\":\"Session not found\"}}\n", req.ID)
		}
		switch req.Method {
		case "initialize":
			reply(`{"protocolVersion":1,"agentCapabilities":{"loadSession":true},"authMethods":[]}`)
		case "session/new":
			// The controlled native fixture owns its snapshot, just as stock
			// Cortex does. The adapter must never synthesize one to admit load.
			var params struct {
				Cwd string `json:"cwd"`
			}
			_ = json.Unmarshal(req.Params, &params)
			dir := filepath.Join(os.Getenv("HOME"), ".snowflake", "cortex", "conversations")
			data, _ := json.Marshal(map[string]any{"session_id": "fixture-native-id", "working_directory": params.Cwd, "history": []any{map[string]any{"id": "prior-user", "role": "user", "content": []any{map[string]any{"type": "text", "text": "prior user"}}}}})
			if os.MkdirAll(dir, 0o700) != nil || os.WriteFile(filepath.Join(dir, "fixture-native-id.json"), data, 0o600) != nil {
				os.Exit(2)
			}
			reply(`{"sessionId":"fixture-native-id","configOptions":` + fixtureOptions + `}`)
		case "session/load":
			var params struct {
				SessionID string `json:"sessionId"`
				Cwd       string `json:"cwd"`
			}
			_ = json.Unmarshal(req.Params, &params)
			if params.SessionID != "fixture-native-id" || params.Cwd != os.Getenv("AO_CORTEX_FIXTURE_WORKSPACE") {
				failure()
				continue
			}
			fmt.Fprintln(os.Stdout, `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"fixture-native-id","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"prior user"}}}}`)
			reply(`{"configOptions":` + fixtureOptions + `}`)
		case "session/set_config_option":
			reply(`{"configOptions":` + fixtureOptions + `}`)
		case "session/prompt":
			pending = append(json.RawMessage(nil), req.ID...)
			fmt.Fprintln(os.Stdout, `{"jsonrpc":"2.0","id":"permission-1","method":"session/request_permission","params":{"sessionId":"fixture-native-id","toolCall":{"toolCallId":"tool-1","title":"Edit fixture","kind":"edit"},"options":[{"optionId":"allow","name":"Allow","kind":"allow_once"},{"optionId":"reject","name":"Reject","kind":"reject_once"}]}}`)
		case "session/cancel":
			if len(pending) > 0 {
				fmt.Fprintf(os.Stdout, "{\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"stopReason\":\"cancelled\"}}\n", pending)
				pending = nil
			}
		case "":
			if string(req.ID) == `"permission-1"` && len(pending) > 0 {
				fmt.Fprintln(os.Stdout, `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"fixture-native-id","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"permission handled"}}}}`)
				fmt.Fprintf(os.Stdout, "{\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"stopReason\":\"end_turn\"}}\n", pending)
				pending = nil
			}
		default:
			if len(req.ID) > 0 {
				reply(`{}`)
			}
		}
	}
}

type fixturePlugin struct{ binary string }

func (p fixturePlugin) ResolveBinary(context.Context) (string, error) { return p.binary, nil }
func (p fixturePlugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	return ports.AgentAuthStatusConfigured, nil
}

func protocolDriver(t *testing.T) (ports.ChatDriver, ports.ChatStartConfig) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := isolatedConfig(t)
	cfg.Env["AO_CORTEX_FIXTURE_WORKSPACE"] = cfg.WorkspacePath
	cfg.Model = "fixture-model"
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := persistenthost.Reconcile(cleanupCtx, cfg.DataDir, map[string]struct{}{}); err != nil {
			t.Errorf("owned fixture cleanup: %v", err)
		}
	})
	return NewCandidate(fixturePlugin{binary: binary}, nil), cfg
}

func closeFixture(t *testing.T, conv ports.ChatConversation) {
	t.Helper()
	if terminator, ok := conv.(ports.ChatProviderTerminator); ok {
		if err := terminator.Terminate(); err != nil {
			t.Errorf("Terminate: %v", err)
		}
	} else {
		_ = conv.Close()
	}
}

func eventUntil(t *testing.T, events <-chan ports.ChatEvent, kind ports.ChatEventKind) ports.ChatEvent {
	t.Helper()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("events closed")
			}
			if event.Err != nil {
				t.Fatal(event.Err)
			}
			if event.Kind == kind {
				return event
			}
		case <-timer.C:
			t.Fatalf("timeout waiting for %s", kind)
		}
	}
}

func TestConstructorRunsNativeACPApprovalAndReconnectLifecycle(t *testing.T) {
	driver, cfg := protocolDriver(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conv, err := driver.Start(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeFixture(t, conv) })
	if _, exposed := conv.(ports.ChatConfigOptionController); exposed {
		t.Fatal("direct config setter bypasses candidate approval policy")
	}
	if conv.ProviderConversationID() != "fixture-native-id" {
		t.Fatalf("identity = %q", conv.ProviderConversationID())
	}
	for _, choice := range []string{"allow", "reject"} {
		ref, err := conv.SendTurn(ctx, ports.ChatUserMessage{Text: "ordinary task", AuthoredByUser: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := conv.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
			t.Fatal(err)
		}
		request := eventUntil(t, conv.Events(), ports.ChatEventApprovalRequested)
		if err := conv.ResolveRequest(ctx, request.RequestID, ports.ChatDecision{ID: choice}); err != nil {
			t.Fatal(err)
		}
		completed := eventUntil(t, conv.Events(), ports.ChatEventTurnCompleted)
		if err := conv.(ports.ChatProviderEventAcknowledger).AcknowledgeProviderEvent(ctx, completed.ProviderEventID); err != nil {
			t.Fatal(err)
		}
		waitFixtureReady(t, conv.Events())
	}
	ref, err := conv.SendTurn(ctx, ports.ChatUserMessage{Text: "cancel pending tool", AuthoredByUser: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := conv.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatal(err)
	}
	request := eventUntil(t, conv.Events(), ports.ChatEventApprovalRequested)
	if err := conv.Interrupt(ctx, request.ProviderTurnID); err != nil {
		t.Fatal(err)
	}
	completed := eventUntil(t, conv.Events(), ports.ChatEventTurnCompleted)
	if completed.TurnState != domain.TurnStateInterrupted {
		t.Fatalf("cancel state = %q", completed.TurnState)
	}
	if err := conv.(ports.ChatProviderEventAcknowledger).AcknowledgeProviderEvent(ctx, completed.ProviderEventID); err != nil {
		t.Fatal(err)
	}
	waitFixtureReady(t, conv.Events())
	assertProtocolChoices(t, cfg)
	if err := conv.Close(); err != nil {
		t.Fatal(err)
	}
	resume := ports.ChatResumeConfig{SessionID: cfg.SessionID, ProviderConversationID: "fixture-native-id", DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath, Env: cfg.Env, HasPriorTurns: true}
	restored, err := driver.(ports.ChatDriverReconnector).Reconnect(ctx, resume)
	if err != nil {
		t.Fatal(err)
	}
	conv = restored
	if live, ok := restored.(ports.ChatLiveReconnector); !ok || !live.ReconnectedLive() {
		t.Fatal("Reconnect did not adopt surviving host")
	}
	if restored.ProviderConversationID() != resume.ProviderConversationID {
		t.Fatal("Reconnect changed exact native ID")
	}
}

func TestConstructorRejectsUnknownModelAndNativeMissingHistory(t *testing.T) {
	driver, cfg := protocolDriver(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cfg.Model = "unadvertised-model"
	if conv, err := driver.Start(ctx, cfg); !errors.Is(err, ports.ErrChatConfigOptionInvalid) {
		if conv != nil {
			closeFixture(t, conv)
		}
		t.Fatalf("unknown model = %v", err)
	}
	// The snapshot exists, but the controlled native returns not-found.
	writeSnapshot(t, cfg, "missing-native-id", map[string]any{"session_id": "missing-native-id", "working_directory": cfg.WorkspacePath})
	// Each failed launch owns a separate AO session and isolated test profile.
	// These snapshots and the native not-found response are controlled fixtures.
	cfg.SessionID = "cortex-missing"
	resume := ports.ChatResumeConfig{SessionID: cfg.SessionID, ProviderConversationID: "missing-native-id", DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath, Env: cfg.Env}
	if conv, err := driver.Resume(ctx, resume); !errors.Is(err, ports.ErrChatResumeFailed) {
		if conv != nil {
			closeFixture(t, conv)
		}
		t.Fatalf("missing history = %v", err)
	}
	calls, err := os.ReadFile(filepath.Join(cfg.Env["HOME"], "protocol-calls"))
	if err != nil {
		t.Fatal(err)
	}
	// Only the earlier deliberately failed Start may call new. Missing restore
	// must not fall back, even when the native error is resource-not-found.
	if strings.Count(string(calls), `"method":"session/new"`) != 1 {
		t.Fatalf("unexpected fresh fallback: %s", calls)
	}
}

func assertProtocolChoices(t *testing.T, cfg ports.ChatStartConfig) {
	t.Helper()
	calls, err := os.ReadFile(filepath.Join(cfg.Env["HOME"], "protocol-calls"))
	if err != nil {
		t.Fatal(err)
	}
	var choices []string
	mode, model, cancel := false, false, false
	for _, line := range strings.Split(strings.TrimSpace(string(calls)), "\n") {
		var message struct {
			Method string `json:"method"`
			Params struct {
				ConfigID string `json:"configId"`
				Value    string `json:"value"`
			} `json:"params"`
			Result struct {
				Outcome struct {
					Outcome  string `json:"outcome"`
					OptionID string `json:"optionId"`
				} `json:"outcome"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			t.Fatal(err)
		}
		switch message.Method {
		case "session/set_config_option":
			if message.Params.ConfigID == "mode" {
				if message.Params.Value != "standard" {
					t.Fatalf("unsafe mode on wire: %s", line)
				}
				mode = true
			}
			if message.Params.ConfigID == "model" && message.Params.Value == "fixture-model" {
				model = true
			}
		case "session/cancel":
			cancel = true
		case "":
			if message.Result.Outcome.Outcome == "selected" {
				choices = append(choices, message.Result.Outcome.OptionID)
			}
		}
	}
	if !reflect.DeepEqual(choices, []string{"allow", "reject"}) || !mode || !model || !cancel {
		t.Fatalf("wire choices=%v standard=%v model=%v cancel=%v", choices, mode, model, cancel)
	}
}

func TestConstructorLoadsExactHistoryAndRejectsWrongWorkspace(t *testing.T) {
	driver, cfg := protocolDriver(t)
	writeSnapshot(t, cfg, "fixture-native-id", nativeSnapshot(cfg))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	resume := ports.ChatResumeConfig{SessionID: cfg.SessionID, ProviderConversationID: "fixture-native-id", DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath, Env: cfg.Env, HasPriorTurns: true}
	conv, err := driver.Resume(ctx, resume)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeFixture(t, conv) })
	if _, exposed := conv.(ports.ChatConfigOptionController); exposed {
		t.Fatal("restore exposes direct config policy bypass")
	}
	if conv.ProviderConversationID() != resume.ProviderConversationID {
		t.Fatal("load changed native ID")
	}
	reader, ok := conv.(ports.ChatHistoryRefresher)
	if !ok {
		t.Fatal("load lost native history refresh interface")
	}
	first, err := reader.ReadHistory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := reader.RefreshHistory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) == 0 || !reflect.DeepEqual(first, second) {
		t.Fatalf("replay changed/duplicated events: first=%+v second=%+v", first, second)
	}
	wrong := resume
	wrong.SessionID = "wrong-workspace"
	wrong.WorkspacePath = t.TempDir()
	if other, err := driver.Resume(ctx, wrong); !errors.Is(err, ports.ErrChatResumeFailed) {
		if other != nil {
			closeFixture(t, other)
		}
		t.Fatalf("wrong-workspace admission = %v", err)
	}
}

// A terminal event precedes releasing the active-turn slot. Receipt ACK is
// durable projection, not native input readiness; wait for the separate signal.
func waitFixtureReady(t *testing.T, events <-chan ports.ChatEvent) {
	t.Helper()
	for {
		event := eventUntil(t, events, ports.ChatEventControllerState)
		if event.ControllerState == ports.ChatControllerReady {
			return
		}
	}
}

func TestConstructorRejectsHistoryFromDifferentPreparedProfile(t *testing.T) {
	driver, cfg := protocolDriver(t)
	writeSnapshot(t, cfg, "fixture-native-id", nativeSnapshot(cfg))
	prepared := cfg
	prepared.Env = map[string]string{"HOME": filepath.Join(cfg.DataDir, "prepared-profile")}
	if err := os.Mkdir(prepared.Env["HOME"], 0o700); err != nil {
		t.Fatal(err)
	}
	wrong := nativeSnapshot(prepared)
	wrong["working_directory"] = t.TempDir()
	writeSnapshot(t, prepared, "fixture-native-id", wrong)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resume := ports.ChatResumeConfig{SessionID: cfg.SessionID, ProviderConversationID: "fixture-native-id", DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath, Env: cfg.Env, HasPriorTurns: true,
		PrepareEnv: func(context.Context) (map[string]string, error) { return prepared.Env, nil }}
	if conv, err := driver.Resume(ctx, resume); !errors.Is(err, ports.ErrChatResumeFailed) {
		if conv != nil {
			closeFixture(t, conv)
		}
		t.Fatalf("prepared profile mismatch = %v", err)
	}
	for _, home := range []string{cfg.Env["HOME"], prepared.Env["HOME"]} {
		if _, err := os.Stat(filepath.Join(home, "protocol-calls")); !os.IsNotExist(err) {
			t.Fatalf("native process started before actual profile admission: %v", err)
		}
	}
}
