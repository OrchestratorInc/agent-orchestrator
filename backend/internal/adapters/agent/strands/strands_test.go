package strands

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func setup(t *testing.T, config string) (*Plugin, ports.LaunchConfig) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, ".strands", "cli", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return &Plugin{resolvedBinary: "strands"}, ports.LaunchConfig{SessionID: "ao-1", NativeSessionID: "native-1", DataDir: t.TempDir(), WorkspacePath: t.TempDir()}
}

func argValue(args []string, flag string) string {
	for i := range args {
		if args[i] == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func TestLaunchPreservesDomainInstructionsAndDefaults(t *testing.T) {
	p, cfg := setup(t, `{"profile":{"instructions":"User domain instructions","model":"anthropic/model"}}`)
	cfg.Prompt = "--not-an-option"
	cfg.SystemPrompt = "Private AO instructions"
	cmd, err := p.GetLaunchCommand(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := argValue(cmd, "--prompt"); got != cfg.Prompt {
		t.Fatalf("prompt=%q", got)
	}
	if got := argValue(cmd, "--instructions"); got != "User domain instructions\n\nPrivate AO instructions" {
		t.Fatalf("instructions=%q", got)
	}
	if argValue(cmd, "--model") != "" {
		t.Fatal("overrode configured model")
	}
	if argValue(cmd, "--session-id") != "native-1" {
		t.Fatal(cmd)
	}
	if !strings.Contains(strings.Join(cmd, " "), "agentConfig.backgroundTasks=false") {
		t.Fatal("foreground dispatch is not enforced")
	}
	if strings.Contains(strings.Join(cmd, " "), "--print") {
		t.Fatal("must remain interactive")
	}
	raw, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".strands", "cli", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "Private AO") {
		t.Fatal("mutated global config")
	}
}

func TestPermissionModesFailClosed(t *testing.T) {
	for _, mode := range []ports.PermissionMode{ports.PermissionModeAcceptEdits, ports.PermissionModeAuto, ports.PermissionModeBypassPermissions} {
		t.Run(string(mode), func(t *testing.T) {
			p, cfg := setup(t, `{}`)
			cfg.Permissions = mode
			if _, err := p.GetLaunchCommand(context.Background(), cfg); err == nil {
				t.Fatal("unsupported mode accepted")
			}
		})
	}
	for _, config := range []string{`{"permissions":{"mode":"bypassPermissions"}}`, `{"permissions":{"allow":["shell"]}}`, `{"agentProject":"./agent.ts"}`} {
		p, cfg := setup(t, config)
		if _, err := p.GetLaunchCommand(context.Background(), cfg); err == nil {
			t.Fatalf("unsafe config accepted: %s", config)
		}
	}
}

func TestExplicitNativeBypassMustMatchAO(t *testing.T) {
	p, cfg := setup(t, `{"permissions":{"mode":"bypassPermissions"}}`)
	cfg.Permissions = ports.PermissionModeBypassPermissions
	if _, err := p.GetLaunchCommand(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
}

func TestModelModuleDoesNotPretendToAcceptOverride(t *testing.T) {
	p, cfg := setup(t, `{"profile":{"modelModule":{"module":"./model.mjs"}}}`)
	cfg.Config.Model = "openai/other"
	if _, err := p.GetLaunchCommand(context.Background(), cfg); err == nil {
		t.Fatal("shadowed override accepted")
	}
}

func TestRestoreRequiresExactNativeCheckpointAndReappliesInstructions(t *testing.T) {
	p, cfg := setup(t, `{"profile":{"instructions":"DOMAIN"}}`)
	if _, err := p.GetLaunchCommand(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	ref := ports.SessionRef{ID: cfg.SessionID, DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "native-1"}}
	restore := ports.RestoreConfig{DataDir: cfg.DataDir, Session: ref, SystemPrompt: "FRESH_AFTER_KILL"}
	if _, ok, err := p.GetRestoreCommand(context.Background(), restore); err == nil || ok {
		t.Fatal("missing checkpoint was accepted")
	}
	path := checkpointPath(cfg.DataDir, "native-1")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"scope":"agent","schemaVersion":"1.0","data":{"messages":[{"role":"user","content":[{"text":"initial task"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd, ok, err := p.GetRestoreCommand(context.Background(), restore)
	if err != nil || !ok {
		t.Fatalf("restore=%v %v", ok, err)
	}
	if argValue(cmd, "--session-id") != "native-1" || argValue(cmd, "--instructions") != "DOMAIN\n\nFRESH_AFTER_KILL" {
		t.Fatal(cmd)
	}
	info, ok, err := p.SessionInfo(context.Background(), ref)
	if err != nil || !ok || info.AgentSessionID != "native-1" {
		t.Fatalf("info=%+v %v %v", info, ok, err)
	}
	restore.Session.WorkspacePath = t.TempDir()
	if _, ok, err := p.GetRestoreCommand(context.Background(), restore); err == nil || ok {
		t.Fatal("workspace mismatch accepted")
	}
}

func TestInvalidIDsAndRestrictions(t *testing.T) {
	for _, id := range []string{"../escape", "-flag", "UPPER", "a/b", ""} {
		p, cfg := setup(t, `{}`)
		cfg.NativeSessionID = id
		if id == "" {
			cfg.SessionID = "../escape"
		}
		if _, err := p.GetLaunchCommand(context.Background(), cfg); err == nil {
			t.Fatalf("accepted %q", id)
		}
	}
	p, cfg := setup(t, `{}`)
	cfg.AllowedTools = []string{"read"}
	if _, err := p.GetLaunchCommand(context.Background(), cfg); err == nil {
		t.Fatal("ignored restrictions")
	}
}

func TestPromptFileAndContext(t *testing.T) {
	p, cfg := setup(t, `{}`)
	cfg.SystemPromptFile = filepath.Join(t.TempDir(), "prompt")
	if _, err := p.GetLaunchCommand(context.Background(), cfg); err == nil {
		t.Fatal("missing prompt ignored")
	}
	if err := os.WriteFile(cfg.SystemPromptFile, []byte("FILE PRIVATE"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd, err := p.GetLaunchCommand(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if argValue(cmd, "--instructions") != "FILE PRIVATE" {
		t.Fatal(cmd)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.GetLaunchCommand(ctx, cfg); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestConfigSpecAndManifest(t *testing.T) {
	p := New()
	spec, err := p.GetConfigSpec(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Fields) != 1 || spec.Fields[0].Key != "model" {
		t.Fatal(spec)
	}
	if p.Manifest().ID != "strands" {
		t.Fatal(p.Manifest())
	}
	if got, _ := p.GetPromptDeliveryStrategy(context.Background(), ports.LaunchConfig{}); got != ports.PromptDeliveryInCommand {
		t.Fatal(got)
	}
}

func TestLaunchLocatorHasOnlyOwnedMetadata(t *testing.T) {
	p, cfg := setup(t, `{}`)
	_, err := p.GetLaunchCommand(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(locatorPath(cfg.DataDir, cfg.SessionID))
	if err != nil {
		t.Fatal(err)
	}
	var actual map[string]string
	if err := json.Unmarshal(data, &actual); err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{"nativeSessionId": "native-1", "workspace": cfg.WorkspacePath}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("got=%v", actual)
	}
}

func TestMalformedMessageHistoryCannotRestoreOrPublishIdentity(t *testing.T) {
	for _, messages := range []string{`null`, `{}`, `"text"`, `[]`, `[null]`, `[{"role":"system","content":[{"text":"x"}]}]`, `[{"role":"user","content":{}}]`, `[{"role":"user","content":[]}]`, `[{"role":"user","content":[null]}]`, `[{"role":"user","content":[{}]}]`, `[{"role":"user","content":[{"text":42}]}]`, `[{"role":"user","content":[{"text":null}]}]`} {
		t.Run(messages, func(t *testing.T) {
			p, cfg := setup(t, `{}`)
			if _, err := p.GetLaunchCommand(context.Background(), cfg); err != nil {
				t.Fatal(err)
			}
			path := checkpointPath(cfg.DataDir, cfg.NativeSessionID)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			body := `{"scope":"agent","schemaVersion":"1.0","data":{"messages":` + messages + `}}`
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			ref := ports.SessionRef{ID: cfg.SessionID, DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: cfg.NativeSessionID}}
			if _, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{DataDir: cfg.DataDir, Session: ref}); err == nil || ok {
				t.Fatal("malformed history accepted for restore")
			}
			if _, ok, err := p.SessionInfo(context.Background(), ref); err == nil || ok {
				t.Fatal("malformed history published native identity")
			}
		})
	}
}

func TestUnsupportedCustomShellSetup(t *testing.T) {
	for _, config := range []string{
		`{"profile":{"agentConfig":{"systemPrompt":"shadow"}}}`,
		`{"profile":{"agentConfigModules":[{"kind":"agent-config","module":"./custom.mjs"}]}}`,
		`{"profile":{"tools":[{"kind":"tool","module":"./custom.mjs"}]}}`,
		`{"profile":{"subagents":[{"kind":"subagent","module":"./custom.mjs"}]}}`,
		`{"profile":{"sandbox":{"kind":"sandbox","module":"./custom.mjs"}}}`,
		`{"profile":{"builtinTools":{"shell":{"description":"custom"}}}}`,
	} {
		p, cfg := setup(t, config)
		if _, err := p.GetLaunchCommand(context.Background(), cfg); err == nil {
			t.Fatal("unsupported custom shell setup accepted")
		}
	}
	for _, config := range []string{`{"profile":{"builtinTools":{"shell":false}}}`, `{"profile":{"builtinTools":["read"]}}`} {
		p, cfg := setup(t, config)
		if _, err := p.GetLaunchCommand(context.Background(), cfg); err != nil {
			t.Fatal(err)
		}
	}
}
