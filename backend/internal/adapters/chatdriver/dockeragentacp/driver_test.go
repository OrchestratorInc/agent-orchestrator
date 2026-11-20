package dockeragentacp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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

// The daemon-wide auth status must not decide admission for a session that
// brings its own credentials: Chat probing is global and cached, while the
// project environment can add a key or override a rejected global one.
func TestProjectCredentialsAdmitDespiteUnauthorizedDaemonStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("qualified native hook binding is Unix only")
	}
	dir := t.TempDir()
	config := filepath.Join(dir, "native.yaml")
	if err := os.WriteFile(config, []byte("agents: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		globalKey string
		env       map[string]string
		wantError bool
	}{
		{name: "project-only key", env: map[string]string{"OPENAI_API_KEY": "project-key"}},
		{name: "project key overrides rejected global key", globalKey: "revoked-key", env: map[string]string{"OPENAI_API_KEY": "project-key"}},
		{name: "no session credentials still refused", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearProviderKeys(t)
			t.Setenv(dockeragent.ConfigEnv, "")
			t.Setenv("ANTHROPIC_API_KEY", tc.globalKey)
			driver := New(installedPlugin{status: ports.AgentAuthStatusUnauthorized}, nil)
			if _, err := driver.Probe(context.Background()); err != nil {
				t.Fatalf("global probe used the daemon's auth status: %v", err)
			}
			err := driver.(ports.ChatEnvironmentValidator).ValidateEnvironment(context.Background(), tc.env)
			if (err != nil) != tc.wantError {
				t.Fatalf("ValidateEnvironment error=%v, wantError=%v", err, tc.wantError)
			}
		})
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
		wantAuth     bool
	}{
		{name: "missing config and provider key", wantError: true, wantAuth: true},
		{name: "provider key uses AO default config", env: map[string]string{"GEMINI_API_KEY": "key"}},
		{name: "relative", env: map[string]string{dockeragent.ConfigEnv: "native.yaml"}, wantError: true},
		{name: "nonexistent", env: map[string]string{dockeragent.ConfigEnv: filepath.Join(dir, "missing.yaml")}, wantError: true},
		{name: "directory", env: map[string]string{dockeragent.ConfigEnv: dir}, wantError: true},
		{name: "daemon config", daemonConfig: config},
		{name: "trimmed daemon config", daemonConfig: "  " + config + " \n"},
		{name: "session config", env: map[string]string{dockeragent.ConfigEnv: config}},
		{name: "session overrides invalid daemon config", daemonConfig: "relative.yaml", env: map[string]string{dockeragent.ConfigEnv: config}},
		{name: "explicit empty override without key", daemonConfig: config, env: map[string]string{dockeragent.ConfigEnv: ""}, wantError: true, wantAuth: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearProviderKeys(t)
			t.Setenv(dockeragent.ConfigEnv, tc.daemonConfig)
			err := validator.ValidateEnvironment(context.Background(), tc.env)
			if (err != nil) != tc.wantError {
				t.Fatalf("ValidateEnvironment error=%v, wantError=%v", err, tc.wantError)
			}
			// No usable key is a setup problem; a bad config leaves the driver unavailable.
			if tc.wantAuth && !errors.Is(err, ports.ErrChatAuthRequired) {
				t.Fatalf("missing provider key not reported as auth-required: %v", err)
			}
			if tc.wantError && !tc.wantAuth && !errors.Is(err, ports.ErrChatDriverUnavailable) {
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
	clearProviderKeys(t)
	for _, cfg := range []acpdriver.LaunchConfig{
		{}, {Env: map[string]string{dockeragent.ConfigEnv: "relative.yaml"}},
		{DataDir: t.TempDir(), SessionID: "s", Env: map[string]string{dockeragent.ConfigEnv: t.TempDir()}},
	} {
		if _, _, err := configure(context.Background(), cfg); err == nil {
			t.Fatal("invalid config admitted")
		}
	}
}

func TestInitializeAdmitsQualifiedAndNewerReleases(t *testing.T) {
	for version, admitted := range map[string]bool{
		"v1.149.9": false, "v0.999.0": false, "": false, "latest": false,
		"v1.150.0": true, "v1.151.0": true, "v1.200.3": true, "v2.0.0": true, "v1.152.0-rc.1": true,
		// A prerelease sorts before its release; build metadata is ignored.
		"v1.150.0-rc.1": false, "1.150.0-beta": false, "v1.149.9-rc.1": false,
		"v1.150.0+build.7": true, "v1.150.0+build-7": true, "v1.150.0-rc.1+build": false,
	} {
		err := validateInitialize(acpsdk.InitializeResponse{AgentInfo: &acpsdk.Implementation{Name: "docker agent", Version: version}, AgentCapabilities: acpsdk.AgentCapabilities{LoadSession: true}})
		if (err == nil) != admitted {
			t.Fatalf("%q: %v", version, err)
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

func TestAdvertisedPermissionModesExcludeAcceptEdits(t *testing.T) {
	if err := validateTurnSettings("", ports.ChatTurnSettings{Approval: ports.PermissionModeAcceptEdits}); !errors.Is(err, ports.ErrChatPermissionModeUnsupported) {
		t.Fatalf("accept-edits error = %v; want ErrChatPermissionModeUnsupported", err)
	}
	checker, ok := New(installedPlugin{}, nil).(ports.ChatPermissionModeChecker)
	if !ok {
		t.Fatal("Docker Agent driver does not report its permission modes")
	}
	if checker.SupportsPermissionMode(ports.PermissionModeAcceptEdits) {
		t.Fatal("accept-edits advertised although launch refuses it")
	}
	for _, mode := range []ports.PermissionMode{ports.PermissionModeDefault, ports.PermissionModeAuto, ports.PermissionModeBypassPermissions} {
		if !checker.SupportsPermissionMode(mode) {
			t.Fatalf("%s not advertised", mode)
		}
	}
}

func TestSessionOptionsLeaveDefaultModelToNativeConfig(t *testing.T) {
	for _, model := range []string{"", "default", " default "} {
		if options := sessionOptions(ports.ChatTurnSettings{Model: model}); options != nil {
			t.Fatalf("%q sent options %+v", model, options)
		}
	}
	want := []acpdriver.SessionOption{{ID: "model", Value: "model:main"}}
	if got := sessionOptions(ports.ChatTurnSettings{Model: "model:main"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("options = %+v, want %+v", got, want)
	}
}

// Model discovery starts a native session, which runs the config's
// session-start hooks, so it must never run in the selected project.
func TestDiscoveryRunsOutsideTheProject(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("qualified native hook binding is Unix only")
	}
	clearProviderKeys(t)
	t.Setenv(dockeragent.ConfigEnv, "")
	dataDir := t.TempDir()
	var launch acpdriver.Launch
	var cwd string
	original := discoverACPConfigOptions
	t.Cleanup(func() { discoverACPConfigOptions = original })
	discoverACPConfigOptions = func(_ context.Context, l acpdriver.Launch, workingDir string, _ *slog.Logger) ([]ports.ChatConfigOption, error) {
		launch, cwd = l, workingDir
		return nil, nil
	}
	if _, err := DiscoverConfigOptions(context.Background(), "/installed/docker-agent", dataDir, map[string]string{"GEMINI_API_KEY": "key"}, nil); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dataDir, "agent-runtime", "docker-agent")
	if !strings.HasPrefix(cwd, root+string(filepath.Separator)) {
		t.Fatalf("discovery cwd %q is not a private directory under %q", cwd, root)
	}
	workingDir := ""
	for i, arg := range launch.Args {
		if arg == "--working-dir" && i+1 < len(launch.Args) {
			workingDir = launch.Args[i+1]
		}
	}
	if workingDir != cwd {
		t.Fatalf("--working-dir = %q, want the discovery directory %q", workingDir, cwd)
	}
	if _, err := os.Stat(cwd); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("discovery directory was not removed: %v", err)
	}
}

func clearProviderKeys(t *testing.T) {
	t.Helper()
	for _, key := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GOOGLE_API_KEY", "GEMINI_API_KEY"} {
		t.Setenv(key, "")
	}
	// Keys saved by `docker-agent setup` are read too; keep the real ones out.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

// Without AO_DOCKER_AGENT_CONFIG, AO writes a private default config for the
// available provider and launches Docker Agent with it.
func TestConfigureWritesDefaultConfigFromProviderKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("qualified native hook binding is Unix only")
	}
	clearProviderKeys(t)
	t.Setenv(dockeragent.ConfigEnv, "")
	cfg := acpdriver.LaunchConfig{DataDir: t.TempDir(), SessionID: "s", WorkspacePath: t.TempDir(), Env: map[string]string{"GEMINI_API_KEY": "secret-value-123"}}
	args, env, err := configure(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if env["GOOGLE_API_KEY"] != "secret-value-123" {
		t.Fatalf("GEMINI_API_KEY not passed on as GOOGLE_API_KEY: %v", env)
	}
	var config string
	for i, arg := range args {
		if arg == "acp" && i+1 < len(args) {
			config = args[i+1]
		}
	}
	b, err := os.ReadFile(config)
	if err != nil || !filepath.IsAbs(config) || !strings.HasPrefix(config, cfg.DataDir) {
		t.Fatalf("default config %q not written: %v", config, err)
	}
	for _, want := range []string{"provider: google", "model: gemini-flash-lite-latest", "model: gemini-flash-latest", "type: filesystem", "type: shell"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("default config missing %q:\n%s", want, b)
		}
	}
	if strings.Contains(string(b), "secret-value-123") {
		t.Fatal("default config must not embed credentials")
	}
}

// The generated default launches with its first model, so a session's own
// provider key must win over a daemon-wide one (which may be revoked), while a
// daemon-only key still works when the session brings none.
func TestDefaultConfigPrefersSessionProviderKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("qualified native hook binding is Unix only")
	}
	for _, tc := range []struct {
		name      string
		daemonKey string
		env       map[string]string
		wantRoot  string
		wantEnv   map[string]string
	}{
		{name: "project OpenAI key over daemon Anthropic key", daemonKey: "revoked", env: map[string]string{"OPENAI_API_KEY": "project-openai"}, wantRoot: "gpt-5.6", wantEnv: map[string]string{}},
		{name: "project Gemini key over daemon Anthropic key", daemonKey: "revoked", env: map[string]string{"GEMINI_API_KEY": "project-gemini"}, wantRoot: "gemini-flash-lite-latest", wantEnv: map[string]string{"GOOGLE_API_KEY": "project-gemini"}},
		{name: "daemon-only key still launches", daemonKey: "daemon-anthropic", env: map[string]string{}, wantRoot: "claude-sonnet-4-6", wantEnv: map[string]string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearProviderKeys(t)
			t.Setenv(dockeragent.ConfigEnv, "")
			t.Setenv("ANTHROPIC_API_KEY", tc.daemonKey)
			cfg := acpdriver.LaunchConfig{DataDir: t.TempDir(), SessionID: "s", WorkspacePath: t.TempDir(), Env: tc.env}
			args, env, err := configure(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			var config string
			for i, arg := range args {
				if arg == "acp" && i+1 < len(args) {
					config = args[i+1]
				}
			}
			b, err := os.ReadFile(config)
			if err != nil {
				t.Fatal(err)
			}
			if want := "agents:\n  root:\n    model: " + tc.wantRoot + "\n"; !strings.Contains(string(b), want) {
				t.Fatalf("root agent does not use %s:\n%s", tc.wantRoot, b)
			}
			delete(env, "TELEMETRY_ENABLED")
			if !reflect.DeepEqual(env, tc.wantEnv) {
				t.Fatalf("launch env overlay = %v, want %v", env, tc.wantEnv)
			}
		})
	}
}

func TestSplitHookContextReassemblesLongContext(t *testing.T) {
	const limit = 1024
	var lines []string
	for i := 0; i < 200; i++ {
		lines = append(lines, fmt.Sprintf("line %d: \"quoted\" <tag> ü %s", i, strings.Repeat("x", i%40)))
	}
	lines = append(lines, strings.Repeat("ü", 3*limit)) // one line longer than a whole response
	text := strings.Join(lines, "\n")
	chunks, err := splitHookContext(text, limit)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 {
		t.Fatalf("long context produced %d response(s)", len(chunks))
	}
	var parts []string
	for _, chunk := range chunks {
		if len(chunk) > limit {
			t.Fatalf("hook response is %d bytes, over %d", len(chunk), limit)
		}
		var out struct {
			HookSpecificOutput struct {
				AdditionalContext string `json:"additional_context"`
			} `json:"hook_specific_output"`
		}
		if err := json.Unmarshal(chunk, &out); err != nil {
			t.Fatal(err)
		}
		parts = append(parts, out.HookSpecificOutput.AdditionalContext)
	}
	// Docker Agent joins hook contexts with newlines; only the over-long
	// line's own splits add newlines that were not in the text.
	joined := strings.Join(parts, "\n")
	if strings.ReplaceAll(joined, "\n", "") != strings.ReplaceAll(text, "\n", "") {
		t.Fatal("reassembled context lost or reordered text")
	}
	if !strings.HasPrefix(joined, strings.Join(lines[:200], "\n")) {
		t.Fatal("splits on newline boundaries did not reassemble exactly")
	}
}

func TestConfigureSplitsLongStandingContextAcrossHooks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("qualified native hook binding is Unix only")
	}
	dir := t.TempDir()
	config := filepath.Join(dir, "native.yaml")
	if err := os.WriteFile(config, []byte("agents: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{dockeragent.ConfigEnv: config}
	prompt := strings.Repeat("Standing instruction line.\n", 4000) // about 108 KiB
	launch := func(prompt string) []string {
		args, _, err := configure(context.Background(), acpdriver.LaunchConfig{DataDir: dir, SessionID: "s", Env: env, SystemPrompt: prompt, WorkspacePath: dir})
		if err != nil {
			t.Fatalf("long standing context refused: %v", err)
		}
		var hooks []string
		for i, arg := range args {
			if arg == "--hook-session-start" {
				hooks = append(hooks, args[i+1])
			}
		}
		return hooks
	}
	if hooks := launch(prompt); len(hooks) < 4 {
		t.Fatalf("108 KiB context used %d hooks", len(hooks))
	}
	// A shorter relaunch leaves no stale responses behind.
	if hooks := launch("short"); len(hooks) != 1 {
		t.Fatalf("short context used %d hooks", len(hooks))
	}
	stale, _ := filepath.Glob(filepath.Join(dir, "agent-runtime", "docker-agent", "*", "standing-context-*.json"))
	if len(stale) != 0 {
		t.Fatalf("stale hook responses kept: %v", stale)
	}
}
