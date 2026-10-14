package dockeragentacp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/dockeragent"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type installedPlugin struct{ status ports.AgentAuthStatus }

func (installedPlugin) ResolveBinary(context.Context) (string, error) {
	return "/installed/docker-agent", nil
}
func (p installedPlugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	return p.status, nil
}

func TestProbeChecksPlatformWithoutRequiringDaemonConfig(t *testing.T) {
	t.Setenv(dockeragent.ConfigEnv, "")
	for _, status := range []ports.AgentAuthStatus{ports.AgentAuthStatusUnknown, ports.AgentAuthStatusConfigured} {
		driver := New(installedPlugin{status: status}, nil)
		caps, err := driver.Probe(context.Background())
		if runtime.GOOS == "windows" {
			if !errors.Is(err, ports.ErrChatDriverUnavailable) || !strings.Contains(err.Error(), "Unix only") || caps != nil {
				t.Fatalf("unsupported platform caps=%v err=%v", caps, err)
			}
		} else if err != nil || !caps[ports.ChatCapabilityResume] {
			t.Fatalf("global probe rejected a possible project config: caps=%v err=%v", caps, err)
		}
	}
}

func TestContextualConfigValidation(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "native.yaml")
	if err := os.WriteFile(config, []byte("agents: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	driver := New(installedPlugin{}, nil)
	validator, ok := driver.(ports.ChatEnvironmentValidator)
	if !ok {
		t.Fatal("Docker Agent binding has no contextual config validation")
	}
	for _, tc := range []struct {
		name         string
		daemonConfig string
		env          map[string]string
		wantError    bool
	}{
		{name: "missing", wantError: true},
		{name: "relative", env: map[string]string{dockeragent.ConfigEnv: "native.yaml"}, wantError: true},
		{name: "nonexistent", env: map[string]string{dockeragent.ConfigEnv: filepath.Join(dir, "missing.yaml")}, wantError: true},
		{name: "directory", env: map[string]string{dockeragent.ConfigEnv: dir}, wantError: true},
		{name: "daemon config", daemonConfig: config},
		{name: "trimmed daemon config", daemonConfig: "  " + config + " \n"},
		{name: "session config", env: map[string]string{dockeragent.ConfigEnv: config}},
		{name: "session overrides invalid daemon config", daemonConfig: "relative.yaml", env: map[string]string{dockeragent.ConfigEnv: config}},
		{name: "explicit empty override", daemonConfig: config, env: map[string]string{dockeragent.ConfigEnv: ""}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(dockeragent.ConfigEnv, tc.daemonConfig)
			err := validator.ValidateEnvironment(context.Background(), tc.env)
			if (err != nil) != tc.wantError {
				t.Fatalf("ValidateEnvironment error=%v, wantError=%v", err, tc.wantError)
			}
			if tc.wantError && !errors.Is(err, ports.ErrChatDriverUnavailable) {
				t.Fatalf("config failure lost typed admission error: %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := validator.ValidateEnvironment(ctx, map[string]string{dockeragent.ConfigEnv: config}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestConfigureRechecksConfigBeforeWritingRuntimeFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("qualified native hook binding is Unix only")
	}
	dir := t.TempDir()
	config := filepath.Join(dir, "native.yaml")
	if err := os.WriteFile(config, []byte("agents: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{dockeragent.ConfigEnv: config}
	if err := validateEnvironment(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(config); err != nil {
		t.Fatal(err)
	}
	_, _, err := configure(context.Background(), acpdriver.LaunchConfig{DataDir: dir, SessionID: "s", Env: env})
	if !errors.Is(err, ports.ErrChatDriverUnavailable) {
		t.Fatalf("removed config admitted at launch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "agent-runtime")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid config created runtime files: %v", err)
	}
}

func TestPrivateHookPreservesNativeConfigAndRefreshesRestoreContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("qualified native hook binding is Unix only")
	}
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
		if err := json.Unmarshal(data, &hook); err != nil || !strings.HasPrefix(hook.Output.Context, prompt+"\n\nAO session working directory: ") || !strings.Contains(hook.Output.Context, cfg.WorkspacePath) {
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
