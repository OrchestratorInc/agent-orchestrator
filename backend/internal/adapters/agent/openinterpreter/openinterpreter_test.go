package openinterpreter

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/pelletier/go-toml/v2"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const nativeUserRecord = `{"timestamp":"2026-10-10T00:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"existing task"}]}}` + "\n"

func TestLaunchPreservesNativeConfigurationAndPromptBoundary(t *testing.T) {
	p := &Plugin{resolvedBinary: "/bin/interpreter"}
	cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		WorkspacePath: t.TempDir(), Prompt: "--dangerously-bypass-hook-trust", SystemPrompt: "private standing instructions",
		Config: ports.AgentConfig{Model: "provider/model"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cmd[len(cmd)-2:], []string{"--", "--dangerously-bypass-hook-trust"}) {
		t.Fatalf("prompt boundary: %q", cmd)
	}
	joined := strings.Join(cmd[:len(cmd)-2], " ")
	for _, forbidden := range []string{"private standing instructions", "developer_instructions", "model_instructions_file", "--dangerously-bypass-hook-trust", "projects="} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("launch unexpectedly contains %q", forbidden)
		}
	}
	if !strings.Contains(joined, "--no-daemon") || !strings.Contains(joined, "--model provider/model") {
		t.Fatalf("launch: %q", cmd)
	}
	strategy, err := p.GetPromptDeliveryStrategy(context.Background(), ports.LaunchConfig{})
	if err != nil || strategy != ports.PromptDeliveryInCommand {
		t.Fatalf("delivery = %q, %v", strategy, err)
	}
}

func TestPermissions(t *testing.T) {
	for _, tc := range []struct {
		mode ports.PermissionMode
		want []string
		fail bool
	}{
		{ports.PermissionModeDefault, nil, false},
		{ports.PermissionModeAcceptEdits, []string{"--sandbox", "workspace-write", "--ask-for-approval", "on-request"}, false},
		{ports.PermissionModeAuto, []string{"--auto-review"}, false},
		{ports.PermissionModeBypassPermissions, []string{"--dangerously-bypass-approvals-and-sandbox"}, false},
		{"typo", nil, true},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			got, err := permissionArgs(tc.mode)
			if (err != nil) != tc.fail || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("args = %q, %v", got, err)
			}
		})
	}
}

func TestBinaryIdentityRejectsPythonInterpreter(t *testing.T) {
	if isRustCLIHelp("Open Interpreter --model --auto-run") || isRustCLIHelp("Codex --no-daemon --chat-completions resume") {
		t.Fatal("accepted a different CLI family")
	}
	if !isRustCLIHelp("Open Interpreter\n--no-daemon\n--chat-completions\nresume") {
		t.Fatal("Rust CLI rejected")
	}
}

func TestRestoreRequiresExactUUIDAndReappliesHooks(t *testing.T) {
	p := &Plugin{resolvedBinary: "/bin/interpreter"}
	const id = "019f706d-1234-7123-8123-123456789abc"
	for _, value := range []string{"", "latest", "../history", "--last"} {
		_, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{Session: ports.SessionRef{Metadata: map[string]string{ports.MetadataKeyAgentSessionID: value}}})
		if ok || err == nil {
			t.Fatalf("restore %q = %v, %v", value, ok, err)
		}
	}
	workspace := t.TempDir()
	cmd, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Env:     map[string]string{"INTERPRETER_HOME": writeNativeHistory(t, id, workspace)},
		Session: ports.SessionRef{WorkspacePath: workspace, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: id}}, Prompt: "-continue",
	})
	if err != nil || !ok {
		t.Fatalf("restore: %v, %v", ok, err)
	}
	if !reflect.DeepEqual(cmd[1:3], []string{"resume", id}) || !reflect.DeepEqual(cmd[len(cmd)-2:], []string{"--", "-continue"}) {
		t.Fatalf("restore argv: %q", cmd)
	}
	if !strings.Contains(strings.Join(cmd, " "), "hooks.SessionStart") {
		t.Fatal("restore lost private context hook")
	}
}

func writeNativeHistory(t *testing.T, id, workspace string) string {
	t.Helper()
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"timestamp": "2026-10-10T00:00:00Z", "type": "session_meta", "payload": sessionMetadata{ID: id, CWD: workspace}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "sessions", "rollout-now-"+id+".jsonl"), []byte(string(data)+"\n"+nativeUserRecord), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestRestoreRejectsMissingAndForeignNativeWorkspace(t *testing.T) {
	const id = "019f706d-1234-7123-8123-123456789abc"
	p := &Plugin{resolvedBinary: "/bin/interpreter"}
	for _, home := range []string{t.TempDir(), writeNativeHistory(t, id, t.TempDir())} {
		_, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{
			Env:     map[string]string{"INTERPRETER_HOME": home},
			Session: ports.SessionRef{WorkspacePath: t.TempDir(), Metadata: map[string]string{ports.MetadataKeyAgentSessionID: id}},
		})
		if err == nil || ok {
			t.Fatal("restored missing or foreign native workspace")
		}
	}
}

func TestCompressedNativeHistoryIsBoundedAndReadOnly(t *testing.T) {
	const id = "019f706d-1234-7123-8123-123456789abc"
	workspace := t.TempDir()
	home := writeNativeHistory(t, id, workspace)
	plain := filepath.Join(home, "sessions", "rollout-now-"+id+".jsonl")
	data, err := os.ReadFile(plain)
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	compressed := encoder.EncodeAll(data, nil)
	if err := os.WriteFile(plain+".zst", compressed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(plain); err != nil {
		t.Fatal(err)
	}
	p := &Plugin{resolvedBinary: "/bin/interpreter"}
	_, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{Env: map[string]string{"INTERPRETER_HOME": home}, Session: ports.SessionRef{WorkspacePath: workspace, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: id}}})
	if err != nil || !ok {
		t.Fatalf("compressed restore = %v, %v", ok, err)
	}
	if _, err := os.Stat(plain); !os.IsNotExist(err) {
		t.Fatal("metadata read materialized native history")
	}
	if current, err := os.ReadFile(plain + ".zst"); err != nil || !reflect.DeepEqual(current, compressed) {
		t.Fatal("metadata read changed compressed history")
	}
	oversize := encoder.EncodeAll([]byte(strings.Repeat("x", 300<<10)), nil)
	if err := os.WriteFile(plain+".zst", oversize, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSessionMetadata(plain + ".zst"); err == nil {
		t.Fatal("unbounded compressed metadata accepted")
	}
}

func TestHookFlagsTrustOnlyExactSessionDefinitions(t *testing.T) {
	var args []string
	appendHookFlags(&args, "/tmp/a<&' executable", false)
	var config map[string]any
	for i := 0; i < len(args); i += 2 {
		if args[i] != "-c" {
			t.Fatalf("flag %q", args[i])
		}
		var layer map[string]any
		if err := toml.Unmarshal([]byte(args[i+1]), &layer); err != nil {
			t.Fatal(err)
		}
		if config == nil {
			config = map[string]any{}
		}
		for key, val := range layer["hooks"].(map[string]any) {
			config[key] = val
		}
	}
	state := config["state"].(map[string]any)
	if len(state) != len(managedHooks) {
		t.Fatalf("trust count %d", len(state))
	}
	for _, hook := range managedHooks {
		group := config[hook.event].([]any)[0].(map[string]any)
		handler := group["hooks"].([]any)[0].(map[string]any)
		// Trust hashes include the upstream-normalized async default.
		handler["async"] = false
		identity := map[string]any{"event_name": hook.key, "hooks": []any{handler}}
		key := "/<session-flags>/config.toml:" + hook.key + ":0:0"
		if state[key].(map[string]any)["trusted_hash"] != hashIdentity(identity) {
			t.Fatalf("wrong trust for %s", key)
		}
	}
	if strings.Contains(strings.Join(args, " "), "dangerously-bypass") {
		t.Fatal("global trust bypass")
	}
}

func TestNativeConfigUsesInvocationEnvironment(t *testing.T) {
	p := New()
	t.Setenv("INTERPRETER_HOME", "/daemon/profile")
	got, err := p.NativeSessionConfigDir(context.Background(), map[string]string{"INTERPRETER_HOME": "/child/profile"})
	if err != nil || got != "/child/profile" {
		t.Fatalf("home = %q, %v", got, err)
	}
}

func TestTranscriptIdentityMustMatchContent(t *testing.T) {
	root := t.TempDir()
	const id = "019f706d-1234-7123-8123-123456789abc"
	path := filepath.Join(root, "sessions", "2026", "10", "10", "rollout-now-"+id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	p := New()
	ref := ports.NativeSessionRef{ConfigDir: root, NativeSessionID: id}
	for _, tc := range []struct {
		metadata string
		want     ports.NativeSessionAvailability
	}{
		{`{"timestamp":"2026-10-10T00:00:00Z","type":"session_meta","payload":{"id":"wrong"}}`, ports.NativeSessionAvailabilityUnavailable},
		{`{"timestamp":"2026-10-10T00:00:00Z","type":"session_meta","payload":{"id":"` + id + `"}}`, ports.NativeSessionAvailabilityAvailable},
	} {
		if err := os.WriteFile(path, []byte(tc.metadata+"\n"+nativeUserRecord), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := p.ProbeNativeSession(context.Background(), ref)
		if err != nil || got != tc.want {
			t.Fatalf("availability = %q, %v", got, err)
		}
	}
	if err := os.Rename(filepath.Join(root, "sessions"), filepath.Join(root, "archived_sessions")); err != nil {
		t.Fatal(err)
	}
	got, err := p.ProbeNativeSession(context.Background(), ref)
	if err != nil || got != ports.NativeSessionAvailabilityUnavailable {
		t.Fatalf("archived = %q, %v", got, err)
	}
}

func TestNativeUserEvidenceRequiresTypedMeaningfulContent(t *testing.T) {
	for _, content := range []string{"", `null`, `{}`, `[]`, `[{"type":"input_text"}]`, `[{"type":"input_text","text":5}]`, `[{"type":"input_text","text":"  "}]`, `[{"type":"unknown","text":"task"}]`, `[{"type":"input_image","image_url":"image","detail":"invalid"}]`} {
		if validNativeUserContent(json.RawMessage(content)) {
			t.Fatalf("accepted invalid user content %q", content)
		}
	}
	for _, content := range []string{`[{"type":"input_text","text":"task"}]`, `[{"type":"input_image","file_id":"file_123"}]`, `[{"type":"input_audio","audio_url":"data:audio/wav;base64,abc"}]`} {
		if !validNativeUserContent(json.RawMessage(content)) {
			t.Fatalf("rejected native content %q", content)
		}
	}
}

func TestRestoreRejectsCorruptOrEmptyNativeConversation(t *testing.T) {
	const id = "019f706d-1234-7123-8123-123456789abc"
	workspace := t.TempDir()
	home := writeNativeHistory(t, id, workspace)
	path := filepath.Join(home, "sessions", "rollout-now-"+id+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	metadataOnly := strings.SplitN(string(data), "\n", 2)[0] + "\n"
	for _, content := range []string{metadataOnly, string(data) + "malformed tail\n"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		p := &Plugin{resolvedBinary: "/bin/interpreter"}
		_, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{Env: map[string]string{"INTERPRETER_HOME": home}, Session: ports.SessionRef{WorkspacePath: workspace, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: id}}})
		if err == nil || ok {
			t.Fatal("restored corrupt or empty native conversation")
		}
	}
}

func TestHooksDoNotClaimAcceptanceOrCompletion(t *testing.T) {
	for _, event := range []string{"session-start", "user-prompt-submit", "permission-request", "stop", "interrupt", "session-end"} {
		if state, ok := DeriveActivityState(event, []byte(`{"turn_id":"turn"}`)); ok {
			t.Fatalf("%s falsely reports %q", event, state)
		}
	}
	if state, ok := DeriveActivityState("post-tool-use", []byte(`{"tool_name":"Bash"}`)); !ok || state != domain.ActivityActive {
		t.Fatalf("tool = %q, %v", state, ok)
	}
	if _, ok := DeriveActivityState("post-tool-use", []byte(`{"agent_id":"child"}`)); ok {
		t.Fatal("child hook changed root activity")
	}
}

func TestAuthNeverTreatsLocalLoginAsAuthorization(t *testing.T) {
	for _, value := range []string{"Logged in using ChatGPT", "Logged in using an API key - sk-...", "Logged in using workload identity"} {
		if got := authStatus([]byte(value)); got != ports.AgentAuthStatusConfigured {
			t.Fatalf("%q = %q", value, got)
		}
	}
	if got := authStatus([]byte("Not logged in")); got != ports.AgentAuthStatusUnknown {
		t.Fatalf("custom providers must remain unknown, got %q", got)
	}
}

func TestRootHookRejectsNestedAndMalformedIdentities(t *testing.T) {
	const root = `"session_id":"019f706d-1234-7123-8123-123456789abc"`
	if !IsRootHook([]byte("{" + root + "}")) {
		t.Fatal("valid root rejected")
	}
	for _, payload := range []string{`{}`, `{"session_id":"bad"}`, "{" + root + `,"agent_id":"child"}`, "{" + root + `,"agent_type":"reviewer"}`, "{" + root + `,"parent_session_id":"parent"}`} {
		if IsRootHook([]byte(payload)) {
			t.Fatalf("accepted child/malformed hook %s", payload)
		}
	}
}
