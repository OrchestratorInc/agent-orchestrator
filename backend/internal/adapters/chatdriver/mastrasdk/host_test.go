//go:build !windows

package mastrasdk

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
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// This controlled protocol fixture exercises the actual candidate constructor
// and shared detached host. It is not native SDK/provider/AO conformance proof.
func TestMain(m *testing.M) {
	if len(os.Args) >= 8 && os.Args[1] == "chat-host" {
		if os.Args[5] != string(persistenthost.ProtocolACP) || os.Args[7] != "--" {
			os.Exit(2)
		}
		err := persistenthost.Run(context.Background(), persistenthost.Config{
			SessionID: os.Args[2], DataDir: os.Args[3], Workdir: os.Args[4],
			Env: os.Environ(), Argv: os.Args[8:], Protocol: persistenthost.ProtocolACP,
			OwnershipFingerprint: os.Args[6],
		})
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if len(os.Args) == 4 && filepath.Base(os.Args[1]) == "entry.mjs" {
		controlledSDKProtocol()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func controlledSDKProtocol() {
	scan := bufio.NewScanner(os.Stdin)
	write := func(value any) { data, _ := json.Marshal(value); _, _ = fmt.Fprintln(os.Stdout, string(data)) }
	update := func(kind, id, text string) {
		write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
			"sessionId": "native-exact", "update": map[string]any{"sessionUpdate": kind, "messageId": id, "content": map[string]any{"type": "text", "text": text}},
		}})
	}
	for scan.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				SessionID string `json:"sessionId"`
			} `json:"params"`
		}
		if json.Unmarshal(scan.Bytes(), &req) != nil || len(req.ID) == 0 {
			continue
		}
		response := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		switch req.Method {
		case "initialize":
			response["result"] = map[string]any{"protocolVersion": 1, "agentInfo": map[string]any{"name": "ao-mastra-sdk-candidate", "version": "1.11.0"}, "agentCapabilities": map[string]any{"loadSession": true}}
		case "session/new":
			response["result"] = map[string]any{"sessionId": "native-exact"}
		case "session/load":
			if req.Params.SessionID != "native-exact" {
				response["error"] = map[string]any{"code": -32602, "message": "missing native history"}
			} else {
				update("user_message_chunk", "native-user", "ordinary task")
				update("agent_message_chunk", "native-assistant", "native answer")
				response["result"] = map[string]any{}
			}
		case "session/prompt":
			update("agent_message_chunk", "native-assistant", "native answer")
			response["result"] = map[string]any{"stopReason": "end_turn"}
		default:
			response["result"] = map[string]any{}
		}
		write(response)
	}
}

func TestCandidatePortsLifecycleThroughControlledPersistentACP(t *testing.T) {
	for _, key := range []string{"MASTRA_APP_DATA_DIR", "MASTRA_DB_PATH", "MASTRA_DB_URL", "MASTRA_STORAGE_BACKEND", "MASTRACODE_YOLO"} {
		t.Setenv(key, "")
	}
	root := t.TempDir()
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = persistenthost.Shutdown(cleanupCtx, root, "owned")
	})
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, filepath.Join(bin, "node")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	native := filepath.Join(root, "native-cli.js")
	if err := os.WriteFile(native, []byte("// controlled protocol fixture, not an installed native SDK\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plugin := &installedPlugin{binary: native}
	driver := NewCandidate(plugin, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	work := t.TempDir()
	start := ports.ChatStartConfig{SessionID: "owned", DataDir: root, WorkspacePath: work, SystemPrompt: "private context"}
	first, err := driver.Start(ctx, start)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.(ports.ChatProviderTerminator).Terminate() })
	if first.ProviderConversationID() != "native-exact" {
		t.Fatal("native identity changed")
	}
	turn, err := first.SendTurn(ctx, ports.ChatUserMessage{Text: "ordinary task"})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.(ports.ChatDeferredTurnStarter).StartDeferredTurn(turn.ProviderTurnID); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case event, ok := <-first.Events():
			if !ok {
				t.Fatal("controlled provider closed before terminal event")
			}
			if event.Kind == ports.ChatEventError {
				t.Fatal(event)
			}
			if event.Kind == ports.ChatEventTurnCompleted {
				goto completed
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
completed:
	if err := first.(ports.ChatProviderTerminator).Terminate(); err != nil {
		t.Fatal(err)
	}
	resume := ports.ChatResumeConfig{SessionID: start.SessionID, DataDir: root, WorkspacePath: work, ProviderConversationID: "native-exact", SystemPrompt: "NEW private context", HasPriorTurns: true}
	second, err := driver.Resume(ctx, resume)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.(ports.ChatProviderTerminator).Terminate() })
	history, err := second.(ports.ChatHistoryReader).ReadHistory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, event := range history {
		text.WriteString(event.Delta)
		text.WriteString(event.Text)
	}
	if !strings.Contains(text.String(), "native answer") {
		t.Fatalf("history replay absent: %+v", history)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := driver.(ports.ChatDriverReconnector).Reconnect(ctx, resume)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = third.(ports.ChatProviderTerminator).Terminate() })
	if third.ProviderConversationID() != "native-exact" {
		t.Fatal("reconnect changed identity")
	}
	if err := third.(ports.ChatProviderTerminator).Terminate(); err != nil {
		t.Fatal(err)
	}
	resume.ProviderConversationID = "missing"
	if _, err := driver.Resume(ctx, resume); !errors.Is(err, ports.ErrChatResumeFailed) {
		t.Fatalf("missing history did not fail closed: %v", err)
	}
}
