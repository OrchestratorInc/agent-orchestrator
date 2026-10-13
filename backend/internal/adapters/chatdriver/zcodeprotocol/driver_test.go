package zcodeprotocol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAdmissionPreservesNativePermissionPolicy(t *testing.T) {
	for _, tc := range []struct {
		mode ports.PermissionMode
		want string
	}{{ports.PermissionModeDefault, "build"}, {ports.PermissionModeAuto, "build"}, {ports.PermissionModeAcceptEdits, "edit"}, {ports.PermissionModeBypassPermissions, "yolo"}} {
		cfg := ports.ChatStartConfig{WorkspacePath: t.TempDir(), DataDir: t.TempDir(), Permissions: tc.mode}
		got, err := validateConfig(cfg)
		if err != nil || got != tc.want {
			t.Fatalf("mode=%s got=%s err=%v", tc.mode, got, err)
		}
		cfg.ReadOnly = true
		if _, err := validateConfig(cfg); err == nil {
			t.Fatal("read-only reviewer silently accepted")
		}
	}
	for _, cfg := range []ports.ChatStartConfig{{WorkspacePath: "relative", DataDir: t.TempDir()}, {WorkspacePath: t.TempDir(), DataDir: t.TempDir(), Model: "unadvertised"}, {WorkspacePath: t.TempDir(), DataDir: t.TempDir(), AdditionalDirectories: []string{t.TempDir()}}} {
		if _, err := validateConfig(cfg); err == nil {
			t.Fatal("unsupported control accepted")
		}
	}
}
func TestMCPFingerprintIsStableAndDoesNotDiscardTransports(t *testing.T) {
	servers := []ports.ChatMCPServerConfig{{Name: "ao", Type: "stdio", Command: "ao", Env: map[string]string{"Z": "z", "A": "a"}}}
	first, err := nativeMCP(servers)
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		next, err := nativeMCP(servers)
		if err != nil || !reflect.DeepEqual(first, next) {
			t.Fatal("MCP launch identity depends on map iteration")
		}
	}
	if _, err := nativeMCP([]ports.ChatMCPServerConfig{{Type: "http", URL: "https://example.invalid"}}); err == nil {
		t.Fatal("unsupported MCP transport silently discarded")
	}
}
func TestHistoryUsesExactNativeBoundariesAndPrivateTurnsStayHidden(t *testing.T) {
	n := nativeEvent{EventID: "native-event", SessionID: "sess_exact", TurnID: "native-turn", Type: "turn.started"}
	p := eventPayload{InputID: "client-input", Input: "--help ordinary task", MessageID: "native-user"}
	events := normalizeEvent(n, p, "client-input", true)
	if len(events) != 2 || events[1].Text != p.Input || events[1].NativeUserMessageID != "native-user" || events[1].NativeTurnID != "native-turn" {
		t.Fatalf("native replay facts lost: %#v", events)
	}
	p.InputVisibility = "model-only"
	if got := normalizeEvent(n, p, "client-input", true); len(got) != 0 {
		t.Fatal("model-only context exposed as user history")
	}
	n.Type = "turn.completed"
	p = eventPayload{Response: "final reply", ResultType: "cancelled"}
	events = normalizeEvent(n, p, "client-input", false)
	if len(events) != 2 || events[0].Text != "final reply" || events[1].TurnState != domain.TurnStateInterrupted {
		t.Fatalf("native cancellation projected as completion: %#v", events)
	}
}

type bufferWriteCloser struct{ bytes.Buffer }

func (*bufferWriteCloser) Close() error { return nil }
func TestDecisionsMustBeOfferedAndInputCannotConsumePermissions(t *testing.T) {
	data := t.TempDir()
	session := "owned"
	dir := filepath.Join(data, "chat-hosts", session)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	descriptor, _ := json.Marshal(persistenthost.Descriptor{PID: 123, StartedAt: time.Unix(100, 0)})
	if err := os.WriteFile(filepath.Join(dir, "host.json"), descriptor, 0o600); err != nil {
		t.Fatal(err)
	}
	prompt := filepath.Join(data, "prompts", session, "system.md")
	if err := os.MkdirAll(filepath.Dir(prompt), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prompt, []byte("bounded synthetic instructions"), 0o600); err != nil {
		t.Fatal(err)
	}
	output := &bufferWriteCloser{}
	c := &conversation{cfg: ports.ChatStartConfig{DataDir: data, SessionID: domain.SessionID(session)}, transport: &persistenthost.Transport{Stdin: output}, permissions: map[string]pendingPermission{"approval": {WireID: json.RawMessage(`"native-rpc"`), Params: permissionParams{RequestID: "approval", Options: []nativeOption{{ID: "reject", Name: "Reject", Response: json.RawMessage(`{"decision":"deny"}`)}}}}}, sent: make(map[string]string), pending: make(map[string]chan frame), events: make(chan ports.ChatEvent, 4), done: make(chan struct{}), log: slog.New(slog.DiscardHandler)}
	if err := c.ResolveRequest(context.Background(), "approval", ports.ChatDecision{ID: "invented-allow"}); !errors.Is(err, ports.ErrChatDecisionNotOffered) {
		t.Fatalf("fabricated decision accepted: %v", err)
	}
	if _, err := c.SendTurn(context.Background(), ports.ChatUserMessage{Text: "yes"}); err == nil {
		t.Fatal("ordinary input consumed a pending native permission")
	}
	if output.Len() != 0 {
		t.Fatal("rejected admission reached native stdin")
	}
	if err := c.ResolveRequest(context.Background(), "approval", ports.ChatDecision{ID: "reject", Raw: []byte(`{"decision":"allow"}`)}); !errors.Is(err, ports.ErrChatDecisionNotOffered) {
		t.Fatal("forged raw response accepted")
	}
	if err := c.ResolveRequest(context.Background(), "approval", ports.ChatDecision{ID: "reject"}); err != nil {
		t.Fatal(err)
	}
	var response struct {
		ID     string
		Result struct{ Decision string }
	}
	if err := json.NewDecoder(output).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.ID != "native-rpc" || response.Result.Decision != "deny" {
		t.Fatalf("provider-owned denial changed: %#v", response)
	}
	if err := c.ResolveRequest(context.Background(), "approval", ports.ChatDecision{ID: "reject"}); !errors.Is(err, ports.ErrChatRequestNotPending) {
		t.Fatal("duplicate decision admitted before native settled it")
	}
	if _, err := io.ReadAll(output); err != nil {
		t.Fatal(err)
	}
}
func TestSnapshotRefusesDifferentNativeHistoryOrWorkspace(t *testing.T) {
	dir := t.TempDir()
	s := nativeSnapshot{}
	s.Protocol.Name = "ZCode Protocol"
	s.Protocol.Version = 1
	s.Session.SessionID = "sess_exact"
	s.Session.Workspace.WorkspacePath = dir
	if err := checkSnapshot(s, "sess_exact", dir); err != nil {
		t.Fatal(err)
	}
	if err := checkSnapshot(s, "sess_missing", dir); err == nil {
		t.Fatal("native identity mismatch accepted")
	}
	if err := checkSnapshot(s, "sess_exact", t.TempDir()); err == nil {
		t.Fatal("foreign native cwd accepted")
	}
}

func TestNativeStreamingHasOneSourceAndSettlesTheSameItem(t *testing.T) {
	n := nativeEvent{EventID: "text-native", SessionID: "sess_exact", TurnID: "turn-native", Type: "model.streaming"}
	p := eventPayload{Kind: "text_delta", Delta: "ordinary live text"}
	delta := normalizeEvent(n, p, "input", false)
	if len(delta) != 1 || delta[0].Kind != ports.ChatEventMessageDelta || delta[0].Delta != p.Delta {
		t.Fatalf("native text lost: %#v", delta)
	}
	n.Type = "part.delta"
	if got := normalizeEvent(n, p, "input", false); len(got) != 0 {
		t.Fatal("duplicate native stream layer emitted twice")
	}
	n.Type = "turn.completed"
	completed := normalizeEvent(n, eventPayload{Response: "ordinary live text", ResultType: "success"}, "input", false)
	if completed[0].ProviderItemID != delta[0].ProviderItemID {
		t.Fatal("final reply created another item instead of settling streamed text")
	}
}
