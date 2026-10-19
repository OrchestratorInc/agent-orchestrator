package stakpakacp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestMain gives the shared detached host its normal dispatch path. The second
// branch is a controlled ACP process, never a Stakpak/native/provider proof.
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
			os.Exit(1)
		}
		os.Exit(0)
	}
	if os.Getenv("AO_STAKPAK_PROTOCOL_TEST") == "1" && len(os.Args) == 4 && os.Args[1] == "--config" && os.Args[3] == "acp" {
		runProtocolHelper()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runProtocolHelper() {
	scanner := bufio.NewScanner(os.Stdin)
	var pending json.RawMessage
	write := func(v any) { _ = json.NewEncoder(os.Stdout).Encode(v) }
	reply := func(id json.RawMessage, result any) {
		write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
	for scanner.Scan() {
		var r struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(scanner.Bytes(), &r) != nil {
			continue
		}
		if r.Method == "" && string(r.ID) == `"native-permission"` && len(pending) != 0 {
			write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "native-controlled-id", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": string(r.Result)}}}})
			reply(pending, map[string]string{"stopReason": "end_turn"})
			pending = nil
			continue
		}
		switch r.Method {
		case "initialize":
			reply(r.ID, map[string]any{"protocolVersion": 1, "agentInfo": map[string]string{"name": "stakpak", "version": helperVersion()}, "agentCapabilities": map[string]any{"loadSession": true, "promptCapabilities": map[string]bool{"image": true}}, "authMethods": []any{}})
		case "session/new":
			reply(r.ID, map[string]string{"sessionId": "native-controlled-id"})
		case "session/prompt":
			pending = append(json.RawMessage(nil), r.ID...)
			write(map[string]any{"jsonrpc": "2.0", "id": "native-permission", "method": "session/request_permission", "params": map[string]any{
				"sessionId": "native-controlled-id", "toolCall": map[string]string{"toolCallId": "controlled-tool", "title": "Controlled tool permission", "kind": "execute"},
				"options": []map[string]string{{"optionId": "allow", "name": "Allow", "kind": "allow_once"}, {"optionId": "reject", "name": "Reject", "kind": "reject_once"}},
			}})
		default:
			if len(r.ID) > 0 {
				write(map[string]any{"jsonrpc": "2.0", "id": r.ID, "error": map[string]any{"code": -32601, "message": "controlled unsupported method"}})
			}
		}
	}
}

func TestCandidateUsesRealSharedTransportForPermissionAndReconnect(t *testing.T) {
	d, cfg := stagedProtocolCandidate(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	first, err := d.Start(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.(ports.ChatProviderTerminator).Terminate() }()
	if first.ProviderConversationID() != "native-controlled-id" {
		t.Fatal("native identity was synthesized")
	}
	runControlledTurn(t, ctx, first, "reject", "first-reject")
	runControlledTurn(t, ctx, first, "allow", "first-allow")
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := d.Reconnect(ctx, ports.ChatResumeConfig{SessionID: "stakpak-controlled", DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath, ProviderConversationID: "native-controlled-id"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.(ports.ChatProviderTerminator).Terminate() }()
	if !second.(ports.ChatLiveReconnector).ReconnectedLive() {
		t.Fatal("did not adopt surviving shared host")
	}
	if err := second.(ports.ChatLiveReconnectActivator).ActivateLiveReconnect(ctx, ""); err != nil {
		t.Fatal(err)
	}
	runControlledTurn(t, ctx, second, "allow", "after-reconnect")
}

func runControlledTurn(t *testing.T, ctx context.Context, c ports.ChatConversation, decision, clientID string) {
	t.Helper()
	ref, err := c.SendTurn(ctx, ports.ChatUserMessage{Text: "ordinary controlled task", ClientMessageID: clientID})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatal(err)
	}
	requested := false
	var response strings.Builder
	for {
		select {
		case <-ctx.Done():
			t.Fatal("controlled lifecycle timed out")
		case event, ok := <-c.Events():
			if !ok {
				t.Fatal("controlled event stream closed")
			}
			if event.Err != nil {
				t.Fatalf("controlled provider: %v", event.Err)
			}
			switch event.Kind {
			case ports.ChatEventApprovalRequested:
				requested = true
				if err := c.ResolveRequest(ctx, event.RequestID, ports.ChatDecision{ID: decision}); err != nil {
					t.Fatal(err)
				}
			case ports.ChatEventMessageDelta:
				response.WriteString(event.Delta)
			case ports.ChatEventTurnCompleted:
				var actual struct {
					Outcome struct {
						Outcome  string `json:"outcome"`
						OptionID string `json:"optionId"`
					} `json:"outcome"`
				}
				if err := json.Unmarshal([]byte(response.String()), &actual); err != nil || !requested || actual.Outcome.Outcome != "selected" || actual.Outcome.OptionID != decision {
					t.Fatalf("native permission response = %q, requested=%v; want selected %q", response.String(), requested, decision)
				}
				if event.ProviderEventID == "" {
					t.Fatal("completion lost persistent event identity")
				}
				if err := c.(ports.ChatProviderEventAcknowledger).AcknowledgeProviderEvent(ctx, event.ProviderEventID); err != nil {
					t.Fatal(err)
				}
				return
			}
		}
	}
}

type protocolPlugin struct{ binary string }

func (p protocolPlugin) ResolveBinary(context.Context) (string, error) { return p.binary, nil }
func (protocolPlugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	return ports.AgentAuthStatusUnknown, nil
}

func helperVersion() string {
	if version := os.Getenv("AO_STAKPAK_PROTOCOL_VERSION"); version != "" {
		return version
	}
	return "0.3.88"
}
func stagedProtocolCandidate(t *testing.T) (*Driver, ports.ChatStartConfig) {
	t.Helper()
	root, workspace := t.TempDir(), t.TempDir()
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = persistenthost.Shutdown(cleanupCtx, root, "stakpak-controlled")
	})
	home := filepath.Join(root, "profile")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("# controlled protocol fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "stakpak-fixture")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.OpenFile(binary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		_ = in.Close()
		t.Fatal(err)
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	_ = in.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatalf("stage protocol helper: %v %v", copyErr, closeErr)
	}

	return New(protocolPlugin{binary: binary}, nil), ports.ChatStartConfig{SessionID: "stakpak-controlled", DataDir: root, WorkspacePath: workspace, Env: map[string]string{"HOME": home, "AO_STAKPAK_PROTOCOL_TEST": "1"}}
}
func TestConstructorRefusesChangedNativeVersionAndExternalExecutable(t *testing.T) {
	t.Run("changed handshake", func(t *testing.T) {
		d, cfg := stagedProtocolCandidate(t)
		cfg.Env["AO_STAKPAK_PROTOCOL_VERSION"] = "99.0.0"
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := d.Start(ctx, cfg); !errors.Is(err, ports.ErrChatDriverIncompatible) {
			t.Fatalf("uninspected version = %v", err)
		}
	})
	t.Run("external installation", func(t *testing.T) {
		_, cfg := stagedProtocolCandidate(t)
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		d := New(protocolPlugin{binary: binary}, nil)
		if _, err := d.Start(context.Background(), cfg); !errors.Is(err, ports.ErrChatDriverUnavailable) {
			t.Fatalf("source executable outside DataDir accepted: %v", err)
		}
	})
}
