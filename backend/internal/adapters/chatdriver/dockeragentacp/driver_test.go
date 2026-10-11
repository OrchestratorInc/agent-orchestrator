package dockeragentacp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/dockeragent"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	acpsdk "github.com/coder/acp-go-sdk"
)

func TestPrivateHookPreservesNativeConfigAndRefreshesRestoreContext(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "user's config.yaml")
	original := []byte("agents:\n  root:\n    instruction: User instructions\n    hooks:\n      session_start: []\n")
	if err := os.WriteFile(config, original, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := acpdriver.LaunchConfig{DataDir: dir, SessionID: "session", WorkspacePath: filepath.Join(dir, "workspace"), SystemPrompt: "PRIVATE_START", Env: map[string]string{dockeragent.ConfigEnv: config}}
	args, env, err := configure(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if env["TELEMETRY_ENABLED"] != "false" || !reflect.DeepEqual(args[4:7], []string{"serve", "acp", config}) {
		t.Fatalf("args=%v env=%v", args, env)
	}
	files, err := filepath.Glob(filepath.Join(dir, "agent-runtime", "docker-agent", "*", "standing-context.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("%v %v", files, err)
	}
	for _, prompt := range []string{"PRIVATE_START", "PRIVATE_RESTORE"} {
		cfg.SystemPrompt = prompt
		if _, _, err := configure(context.Background(), cfg); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(files[0])
		if err != nil {
			t.Fatal(err)
		}
		var hook struct {
			Output struct {
				Context string `json:"additional_context"`
			} `json:"hook_specific_output"`
		}
		if err := json.Unmarshal(data, &hook); err != nil || hook.Output.Context != prompt {
			t.Fatalf("hook=%s err=%v", data, err)
		}
		info, _ := os.Stat(files[0])
		if info.Mode().Perm() != 0o600 {
			t.Fatal(info.Mode())
		}
	}
	after, _ := os.ReadFile(config)
	if !reflect.DeepEqual(after, original) {
		t.Fatal("native config modified")
	}
	if strings.Contains(strings.Join(args, " "), "PRIVATE") {
		t.Fatal("private context exposed in argv")
	}
	cfg.ProviderScopeID = "other"
	if _, _, err := configure(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	files, _ = filepath.Glob(filepath.Join(dir, "agent-runtime", "docker-agent", "*", "standing-context.json"))
	if len(files) != 2 {
		t.Fatal("scopes share storage")
	}
}

func TestConfigureFailsClosed(t *testing.T) {
	for _, cfg := range []acpdriver.LaunchConfig{
		{}, {Env: map[string]string{dockeragent.ConfigEnv: "relative.yaml"}},
		{DataDir: t.TempDir(), SessionID: "s", Env: map[string]string{dockeragent.ConfigEnv: t.TempDir()}},
	} {
		if _, _, err := configure(context.Background(), cfg); err == nil {
			t.Fatal("invalid config admitted")
		}
	}
}

func TestInitializePinsNativeDistribution(t *testing.T) {
	for _, version := range []string{"v1.149.0", "v1.150.0", "v1.151.0"} {
		err := validateInitialize(acpsdk.InitializeResponse{AgentInfo: &acpsdk.Implementation{Name: "docker agent", Version: version}, AgentCapabilities: acpsdk.AgentCapabilities{LoadSession: true}})
		if (err == nil) != (version == "v1.150.0") {
			t.Fatalf("%s: %v", version, err)
		}
	}
	if validateInitialize(acpsdk.InitializeResponse{}) == nil {
		t.Fatal("missing identity admitted")
	}
}

func TestUnsupportedTurnSettingsAreRejected(t *testing.T) {
	for _, settings := range []ports.ChatTurnSettings{{Effort: "high"}, {Approval: ports.PermissionModeAcceptEdits}, {Model: "arbitrary"}} {
		if err := validateTurnSettings("", settings); err == nil {
			t.Fatalf("admitted %+v", settings)
		}
	}
	for mode, want := range map[ports.PermissionMode]string{ports.PermissionModeDefault: "default", ports.PermissionModeAuto: "balanced", ports.PermissionModeBypassPermissions: "autonomous"} {
		if got := sessionMode(mode); got != want {
			t.Fatalf("%s: %s", mode, got)
		}
	}
}
