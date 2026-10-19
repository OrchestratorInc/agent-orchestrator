package hermesacp

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// CI-only controlled protocol coverage. This is not real Hermes qualification.
func TestMain(m *testing.M) {
	if len(os.Args) > 7 && os.Args[1] == "chat-host" {
		err := persistenthost.Run(context.Background(), persistenthost.Config{SessionID: os.Args[2], DataDir: os.Args[3], Workdir: os.Args[4], Protocol: persistenthost.ProtocolACP, OwnershipFingerprint: os.Args[6], Argv: os.Args[8:], Env: os.Environ()})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Args[1] == "acp" {
		protocolFixture()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func protocolFixture() {
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			continue
		}
		calls, err := os.OpenFile(filepath.Join(os.Getenv("HERMES_HOME"), "fixture-calls"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			os.Exit(5)
		}
		_, _ = fmt.Fprintln(calls, request.Method)
		_ = calls.Close()
		var result any = map[string]any{}
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": 1, "agentInfo": map[string]string{"name": "hermes-agent", "version": "0.21.6"}, "agentCapabilities": map[string]any{"loadSession": true}}
		case "session/new":
			result = map[string]any{"sessionId": "native-id", "modes": map[string]any{"currentModeId": "default", "availableModes": []map[string]string{{"id": "default", "name": "Default"}}}}
		case "session/load":
			result = map[string]any{"configOptions": []map[string]any{{"id": "mode", "name": "Approval", "type": "select", "currentValue": "default", "options": []map[string]string{{"value": "default", "name": "Default"}, {"value": "dont_ask", "name": "Do not ask"}}}}}
			var p struct {
				ID string `json:"sessionId"`
			}
			_ = json.Unmarshal(request.Params, &p)
			if p.ID != "native-id" {
				os.Exit(3)
			}
		case "session/set_config_option":
			os.Exit(4) // any direct setter traffic violates the candidate policy
		case "session/prompt":
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "native-id", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": "controlled reply"}}}})
			result = map[string]string{"stopReason": "end_turn"}
		}
		if len(request.ID) > 0 {
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		}
	}
}

type fixturePlugin struct{}

func (fixturePlugin) ResolveBinary(context.Context) (string, error) { return os.Executable() }
func (fixturePlugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	return ports.AgentAuthStatusConfigured, nil
}

func waitFor(t *testing.T, c ports.ChatConversation, kind ports.ChatEventKind) {
	t.Helper()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-c.Events():
			if !ok {
				t.Fatal("closed before expected event")
			}
			if event.Kind == kind {
				return
			}
		case <-timer.C:
			t.Fatalf("missing %s", kind)
		}
	}
}
func TestConstructorStartSendDetachReconnectAndExactResume(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	data, workspace := t.TempDir(), t.TempDir()
	home := provision(t, data, "lifecycle")
	env := map[string]string{"HERMES_HOME": home}
	driver := New(fixturePlugin{}, nil)
	first, err := driver.Start(ctx, ports.ChatStartConfig{SessionID: "lifecycle", DataDir: data, WorkspacePath: workspace, Env: env, SystemPrompt: "initial"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.(ports.ChatProviderTerminator).Terminate() }()
	if first.ProviderConversationID() != "native-id" {
		t.Fatal("native identity substituted")
	}
	ref, err := first.SendTurn(ctx, ports.ChatUserMessage{Text: "ordinary task"})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, first, ports.ChatEventTurnCompleted)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := ports.ChatResumeConfig{SessionID: "lifecycle", DataDir: data, WorkspacePath: workspace, Env: env, ProviderConversationID: "native-id", SystemPrompt: "new after kill", HasPriorTurns: true}
	live, err := driver.Reconnect(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = live.(ports.ChatProviderTerminator).Terminate() }()
	if !live.(ports.ChatLiveReconnector).ReconnectedLive() {
		t.Fatal("replacement process spawned")
	}
	raw, err := os.ReadFile(filepath.Join(home, "plugins", pluginName, "instructions.txt"))
	if err != nil || string(raw) != "initial" {
		t.Fatal("reconnect mutated live profile")
	}
	if err := live.(ports.ChatProviderTerminator).Terminate(); err != nil {
		t.Fatal(err)
	}
	// Native history is seeded only for this controlled server. Production never
	// writes or synthesizes native transcript identity/history.
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(map[string]string{"cwd": workspace})
	for _, q := range []string{`CREATE TABLE sessions(id TEXT PRIMARY KEY,source TEXT,cwd TEXT,model_config TEXT,parent_session_id TEXT)`, `CREATE TABLE messages(session_id TEXT,active INTEGER)`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO sessions VALUES('native-id','acp',?,?,NULL)`, workspace, string(meta)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO messages VALUES('native-id',1)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if err := os.WriteFile(filepath.Join(home, "fixture-calls"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	restored, err := driver.Resume(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restored.(ports.ChatProviderTerminator).Terminate() }()
	calls, err := os.ReadFile(filepath.Join(home, "fixture-calls"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(calls), "session/load\n") || strings.Contains(string(calls), "session/new\n") || strings.Contains(string(calls), "session/resume\n") {
		t.Fatalf("incorrect restore wire: %s", calls)
	}
	if restored.ProviderConversationID() != "native-id" {
		t.Fatal("restore changed identity")
	}
	raw, err = os.ReadFile(filepath.Join(home, "plugins", pluginName, "instructions.txt"))
	if err != nil || string(raw) != cfg.SystemPrompt {
		t.Fatal("restore failed to refresh private instructions")
	}
	if _, err := restored.(ports.ChatConfigOptionController).SetConfigOption(ctx, "mode", ports.ChatConfigOptionValue{Select: "dont_ask"}); !errors.Is(err, ports.ErrChatConfigOptionInvalid) {
		t.Fatalf("direct policy bypass: %v", err)
	}
}
