package kimi

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestManifest(t *testing.T) {
	m := (&Plugin{}).Manifest()
	if m.ID != "kimi" {
		t.Fatalf("ID = %q, want kimi", m.ID)
	}
	if m.Name != "Kimi" {
		t.Fatalf("Name = %q, want Kimi", m.Name)
	}
	hasAgent := false
	for _, c := range m.Capabilities {
		if c == adapters.CapabilityAgent {
			hasAgent = true
		}
	}
	if !hasAgent {
		t.Fatal("missing CapabilityAgent")
	}
}

func TestKimiBinarySpecIncludesSelfManagedInstallPath(t *testing.T) {
	want := []string{".kimi-code", "bin", "kimi"}
	for _, path := range kimiBinarySpec.UnixHomePaths {
		if reflect.DeepEqual(path, want) {
			return
		}
	}
	t.Fatalf("UnixHomePaths = %#v, want %v", kimiBinarySpec.UnixHomePaths, want)
}

func TestGetConfigSpecAdvertisesModelField(t *testing.T) {
	spec, err := (&Plugin{}).GetConfigSpec(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(spec.Fields) != 1 {
		t.Fatalf("expected 1 field, got %d", len(spec.Fields))
	}
	if spec.Fields[0].Key != "model" || spec.Fields[0].Type != ports.ConfigFieldString {
		t.Fatalf("field = %#v, want model/string", spec.Fields[0])
	}
}

func TestGetLaunchCommandAppendsModelFlagWhenConfigured(t *testing.T) {
	p := &Plugin{resolvedBinary: "kimi"}
	cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Config: ports.AgentConfig{Model: "k2.5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"kimi", "--model", "k2.5"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestGetLaunchCommandOmitsModelFlagWhenBlank(t *testing.T) {
	p := &Plugin{resolvedBinary: "kimi"}
	cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Config: ports.AgentConfig{Model: "   "},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"kimi"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestGetLaunchCommandModelFlagComposesWithApprovalFlags(t *testing.T) {
	p := &Plugin{resolvedBinary: "kimi"}
	cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Permissions: ports.PermissionModeBypassPermissions,
		Config:      ports.AgentConfig{Model: "k2.5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"kimi", "-y", "--model", "k2.5"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

// Kimi Code 2.2 accepts model overrides alongside exact session resume.
func TestGetRestoreCommandPreservesModelOverride(t *testing.T) {
	p := &Plugin{resolvedBinary: "kimi"}
	cmd, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Session: ports.SessionRef{
			Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "01HZABC"},
		},
		Config: ports.AgentConfig{Model: "k2.5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("ok=false, want true")
	}
	want := []string{"kimi", "--model", "k2.5", "--session", "01HZABC"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestGetPromptDeliveryStrategy(t *testing.T) {
	s, err := (&Plugin{}).GetPromptDeliveryStrategy(context.Background(), ports.LaunchConfig{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if s != ports.PromptDeliveryAfterStart {
		t.Fatalf("strategy = %q, want %q", s, ports.PromptDeliveryAfterStart)
	}
}

func TestPromptReadinessHints(t *testing.T) {
	hints, err := (&Plugin{}).PromptReadinessHints(context.Background(), ports.LaunchConfig{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if hints.InitialDelay != 750*time.Millisecond || hints.PollInterval != 200*time.Millisecond || hints.Timeout != 90*time.Second || hints.Lines != 80 {
		t.Fatalf("hints = %#v", hints)
	}
	if !reflect.DeepEqual(hints.Patterns, []string{"│ >"}) {
		t.Fatalf("patterns = %#v, want Kimi composer prompt", hints.Patterns)
	}
}

// Kimi prompt mode is non-interactive, so AO launches the TUI and lets the
// session manager inject the task after startup. Because the prompt is not
// carried with `-p`, approval flags remain valid for prompted workers.
func TestGetLaunchCommandInteractiveMapsPermissionModes(t *testing.T) {
	tests := []struct {
		name       string
		mode       ports.PermissionMode
		prompt     string
		want       []string
		wantAbsent string
	}{
		{"default omits flag", ports.PermissionModeDefault, "fix it", []string{"kimi"}, "--auto"},
		{"empty omits flag", "", "fix it", []string{"kimi"}, "--auto"},
		{"accept edits", ports.PermissionModeAcceptEdits, "-add a health check", []string{"kimi", "--auto"}, "-y"},
		{"auto", ports.PermissionModeAuto, "fix it", []string{"kimi", "--auto"}, "-y"},
		{"bypass", ports.PermissionModeBypassPermissions, "fix it", []string{"kimi", "-y"}, "--auto"},
		{"promptless interactive", ports.PermissionModeAuto, "", []string{"kimi", "--auto"}, "-p"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{resolvedBinary: "kimi"}
			cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{Permissions: tt.mode, Prompt: tt.prompt})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cmd, tt.want) {
				t.Fatalf("cmd = %#v, want %#v", cmd, tt.want)
			}
			for _, arg := range cmd {
				if arg == "-p" || arg == "--prompt" {
					t.Fatalf("cmd = %#v unexpectedly uses non-interactive prompt mode", cmd)
				}
				if tt.wantAbsent != "" && arg == tt.wantAbsent {
					t.Fatalf("cmd = %#v unexpectedly contains %q", cmd, tt.wantAbsent)
				}
			}
		})
	}
}

func TestGetLaunchCommandIgnoresSystemPrompt(t *testing.T) {
	p := &Plugin{resolvedBinary: "kimi"}
	cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		SystemPrompt:     "follow repo rules",
		SystemPromptFile: "/tmp/system.md",
		Prompt:           "do the thing",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Kimi has no documented system-prompt flag, and prompted tasks are injected
	// after startup rather than through non-interactive `-p`.
	want := []string{"kimi"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

// Kimi docs: `--yolo` and `--auto` cannot be used together with `--continue`
// or `--session` — resumed sessions inherit the approval settings of the
// original session — so the restore path must not emit approval flags
// regardless of the requested AO PermissionMode.
func TestGetRestoreCommand(t *testing.T) {
	modes := []ports.PermissionMode{
		ports.PermissionModeDefault,
		"",
		ports.PermissionModeAcceptEdits,
		ports.PermissionModeAuto,
		ports.PermissionModeBypassPermissions,
	}

	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			p := &Plugin{resolvedBinary: "kimi"}
			cmd, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{
				Session: ports.SessionRef{
					Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "01HZABC"},
				},
				Permissions: mode,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				t.Fatal("ok=false, want true")
			}

			want := []string{"kimi"}
			switch mode {
			case ports.PermissionModeAcceptEdits, ports.PermissionModeAuto:
				want = append(want, "--auto")
			case ports.PermissionModeBypassPermissions:
				want = append(want, "-y")
			}
			want = append(want, "--session", "01HZABC")
			if !reflect.DeepEqual(cmd, want) {
				t.Fatalf("cmd = %#v, want %#v", cmd, want)
			}

		})
	}
}

func TestGetRestoreCommandNoID(t *testing.T) {
	p := &Plugin{resolvedBinary: "kimi"}

	cases := []struct {
		name string
		ref  ports.SessionRef
	}{
		{"empty session ref", ports.SessionRef{}},
		{"empty metadata", ports.SessionRef{Metadata: map[string]string{}}},
		{"blank agent session metadata", ports.SessionRef{Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "   "}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{Session: tc.ref})
			if err != nil {
				t.Fatal(err)
			}
			if ok {
				t.Fatal("ok=true with no agentSessionId, want false")
			}
			if cmd != nil {
				t.Fatalf("cmd = %#v, want nil", cmd)
			}
		})
	}
}

func TestGetAgentHooksInstallsSystemPromptInstructions(t *testing.T) {
	workspace := t.TempDir()
	kimiHome := t.TempDir()

	if err := (&Plugin{}).GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{
		WorkspacePath: workspace,
		SystemPrompt:  "follow AO rules\n",
		Env:           map[string]string{"KIMI_CODE_HOME": kimiHome},
	}); err != nil {
		t.Fatalf("GetAgentHooks err = %v", err)
	}

	path := kimiInstructionsPath(workspace)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read instructions: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		kimiInstructionsSentinel,
		"# Agent Orchestrator Session Instructions",
		"follow AO rules",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("instructions missing %q:\n%s", want, text)
		}
	}

	gitignore, err := os.ReadFile(filepath.Join(workspace, kimiInstructionsDirName, ".gitignore"))
	if err != nil {
		t.Fatalf("read gitignore: %v", err)
	}
	if !strings.Contains(string(gitignore), "/AGENTS.md\n") {
		t.Fatalf("gitignore does not ignore AGENTS.md:\n%s", gitignore)
	}
}

func TestPrepareACPInstructionsHonorsCanceledContextBeforeFilesystemWrites(t *testing.T) {
	workspace := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := PrepareACPInstructions(ctx, workspace, "AO worker instructions")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PrepareACPInstructions error = %v, want context.Canceled", err)
	}
	if _, statErr := os.Stat(filepath.Join(workspace, kimiInstructionsDirName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Kimi instruction directory was created after cancellation: %v", statErr)
	}
}

func TestKimiInstructionsPathUsesProviderDiscoveredDirectory(t *testing.T) {
	workspace := t.TempDir()
	got := kimiInstructionsPath(workspace)
	want := filepath.Join(workspace, ".kimi", "AGENTS.md")
	if got != want {
		t.Fatalf("kimiInstructionsPath = %q, want provider-discovered path %q", got, want)
	}
}

func TestGetAgentHooksInstallsKimiConfigHooksWithoutSystemPrompt(t *testing.T) {
	workspace := t.TempDir()
	kimiHome := t.TempDir()
	configPath := filepath.Join(kimiHome, "config.toml")
	existing := `default_model = "kimi-code/kimi-for-coding"

[[hooks]]
event = "Notification"
matcher = "task\\.completed"
command = "notify-send done"
timeout = 7
`
	if err := os.WriteFile(configPath, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := (&Plugin{}).GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{
		WorkspacePath: workspace,
		Env:           map[string]string{"KIMI_CODE_HOME": kimiHome},
	}); err != nil {
		t.Fatalf("GetAgentHooks err = %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		`default_model = "kimi-code/kimi-for-coding"`,
		`command = "notify-send done"`,
		kimiHooksSentinelStart,
		`event = "SessionStart"`,
		`matcher = "startup"`,
		`command = "ao hooks kimi session-start"`,
		`event = "UserPromptSubmit"`,
		`command = "ao hooks kimi user-prompt-submit"`,
		`event = "PermissionRequest"`,
		`command = "ao hooks kimi permission-request"`,
		`event = "Stop"`,
		`command = "ao hooks kimi stop"`,
		kimiHooksSentinelEnd,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("config missing %q:\n%s", want, text)
		}
	}
	if _, err := os.Stat(kimiInstructionsPath(workspace)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("instructions path err = %v, want not exist", err)
	}
}

func TestGetAgentHooksSeedsAOManagedConfigFromUserKimiConfig(t *testing.T) {
	workspace := t.TempDir()
	userHome := t.TempDir()
	aoHome := t.TempDir()
	t.Setenv(kimiCodeHomeEnv, userHome)
	userConfig := `api_key = "user-key"
default_model = "kimi-code/kimi-for-coding"
`
	if err := os.WriteFile(filepath.Join(userHome, "config.toml"), []byte(userConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := (&Plugin{}).GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{
		WorkspacePath: workspace,
		Env:           map[string]string{kimiCodeHomeEnv: aoHome},
	}); err != nil {
		t.Fatalf("GetAgentHooks err = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(aoHome, "config.toml"))
	if err != nil {
		t.Fatalf("read AO config: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		`api_key = "user-key"`,
		`default_model = "kimi-code/kimi-for-coding"`,
		`command = "ao hooks kimi session-start"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("AO config missing %q:\n%s", want, text)
		}
	}
	source, err := os.ReadFile(filepath.Join(userHome, "config.toml"))
	if err != nil {
		t.Fatalf("read source config: %v", err)
	}
	if string(source) != userConfig {
		t.Fatalf("source config mutated:\n%s", source)
	}
}

const kimiOAuthUserConfig = `default_model = "kimi-code/kimi-for-coding"

[providers."managed:kimi-code"]
api_key = ""

[providers."managed:kimi-code".oauth]
storage = "file"
key = "oauth/kimi-code"

[models."kimi-code/kimi-for-coding"]
provider = "managed:kimi-code"
`

func TestGetAgentHooksSeedsAOManagedConfigFromOAuthUserKimiHome(t *testing.T) {
	workspace := t.TempDir()
	userHome := t.TempDir()
	aoHome := t.TempDir()
	t.Setenv(kimiCodeHomeEnv, userHome)
	userCredentials := []byte(`{"refresh_token":"user-refresh"}`)
	writeKimiOAuthProfile(t, userHome, userCredentials)

	if err := (&Plugin{}).GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{
		WorkspacePath: workspace,
		Env:           map[string]string{kimiCodeHomeEnv: aoHome},
	}); err != nil {
		t.Fatalf("GetAgentHooks err = %v", err)
	}

	assertKimiOAuthProfileSeeded(t, userHome, aoHome, userCredentials, userCredentials)
}

func TestGetAgentHooksSeedsCredentialReferencedByOAuthConfig(t *testing.T) {
	for _, tc := range []struct {
		name               string
		writeDefaultSource bool
	}{
		{name: "scoped credential only"},
		{name: "scoped and unrelated default credentials", writeDefaultSource: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			userHome := t.TempDir()
			aoHome := t.TempDir()
			t.Setenv(kimiCodeHomeEnv, userHome)

			const scopedName = "kimi-code-env-a1b2c3"
			userConfig := strings.Replace(
				kimiOAuthUserConfig,
				`key = "oauth/kimi-code"`,
				`key = "oauth/`+scopedName+`"`,
				1,
			)
			if err := os.WriteFile(filepath.Join(userHome, "config.toml"), []byte(userConfig), 0o600); err != nil {
				t.Fatal(err)
			}
			credentialsDir := filepath.Join(userHome, "credentials")
			if err := os.MkdirAll(credentialsDir, 0o700); err != nil {
				t.Fatal(err)
			}
			scopedCredentials := []byte(`{"refresh_token":"scoped-refresh"}`)
			if err := os.WriteFile(filepath.Join(credentialsDir, scopedName+".json"), scopedCredentials, 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.writeDefaultSource {
				if err := os.WriteFile(
					filepath.Join(credentialsDir, "kimi-code.json"),
					[]byte(`{"access_token":"unrelated-default"}`),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			}

			if err := (&Plugin{}).GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{
				WorkspacePath: workspace,
				Env:           map[string]string{kimiCodeHomeEnv: aoHome},
			}); err != nil {
				t.Fatalf("GetAgentHooks err = %v", err)
			}

			config, err := os.ReadFile(filepath.Join(aoHome, "config.toml"))
			if err != nil {
				t.Fatalf("read AO config: %v", err)
			}
			if !strings.Contains(string(config), `default_model = "kimi-code/kimi-for-coding"`) {
				t.Fatalf("AO config did not seed the scoped OAuth profile:\n%s", config)
			}
			targetScopedPath := filepath.Join(aoHome, "credentials", scopedName+".json")
			gotCredentials, err := os.ReadFile(targetScopedPath)
			if err != nil {
				t.Fatalf("read AO scoped credentials: %v", err)
			}
			if string(gotCredentials) != string(scopedCredentials) {
				t.Fatalf("AO scoped credentials = %s, want %s", gotCredentials, scopedCredentials)
			}
			if tc.writeDefaultSource {
				// A user signed in to Kimi shares their whole store with AO's Kimi.
				assertKimiCredentialsShared(t, userHome, aoHome)
			} else if _, err := os.Stat(filepath.Join(aoHome, "credentials", "kimi-code.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unreferenced default AO credential stat err = %v, want not exist", err)
			}
		})
	}
}

func TestGetAgentHooksReseedsHookOnlyAOManagedConfigFromOAuthUserKimiHome(t *testing.T) {
	workspace := t.TempDir()
	userHome := t.TempDir()
	aoHome := t.TempDir()
	t.Setenv(kimiCodeHomeEnv, userHome)
	userCredentials := []byte(`{"access_token":"user-access"}`)
	aoCredentials := []byte(`{"refresh_token":"ao-refresh"}`)
	writeKimiOAuthProfile(t, userHome, userCredentials)
	if err := os.WriteFile(filepath.Join(aoHome, "config.toml"), []byte(kimiHooksConfigBlock()), 0o600); err != nil {
		t.Fatal(err)
	}
	credentialsPath := filepath.Join(aoHome, "credentials", "kimi-code.json")
	if err := os.MkdirAll(filepath.Dir(credentialsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credentialsPath, aoCredentials, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := (&Plugin{}).GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{
		WorkspacePath: workspace,
		Env:           map[string]string{kimiCodeHomeEnv: aoHome},
	}); err != nil {
		t.Fatalf("GetAgentHooks err = %v", err)
	}

	// AO's stale copy gives way to the user's own login.
	assertKimiOAuthProfileSeeded(t, userHome, aoHome, userCredentials, userCredentials)
	assertKimiCredentialsShared(t, userHome, aoHome)
}

func writeKimiOAuthProfile(t *testing.T, home string, credentials []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(kimiOAuthUserConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	credentialsPath := filepath.Join(home, "credentials", "kimi-code.json")
	if err := os.MkdirAll(filepath.Dir(credentialsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credentialsPath, credentials, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertKimiOAuthProfileSeeded(
	t *testing.T,
	userHome, aoHome string,
	userCredentials, wantAOCredentials []byte,
) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(aoHome, "config.toml"))
	if err != nil {
		t.Fatalf("read AO config: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		`default_model = "kimi-code/kimi-for-coding"`,
		`[providers."managed:kimi-code".oauth]`,
		`[models."kimi-code/kimi-for-coding"]`,
		`command = "ao hooks kimi session-start"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("AO config missing %q:\n%s", want, text)
		}
	}
	if got := strings.Count(text, kimiHooksSentinelStart); got != 1 {
		t.Fatalf("managed hook block count = %d, want 1:\n%s", got, text)
	}

	credentials, err := os.ReadFile(filepath.Join(aoHome, "credentials", "kimi-code.json"))
	if err != nil {
		t.Fatalf("read AO credentials: %v", err)
	}
	if string(credentials) != string(wantAOCredentials) {
		t.Fatalf("AO credentials = %s, want %s", credentials, wantAOCredentials)
	}

	sourceConfig, err := os.ReadFile(filepath.Join(userHome, "config.toml"))
	if err != nil {
		t.Fatalf("read source config: %v", err)
	}
	if string(sourceConfig) != kimiOAuthUserConfig {
		t.Fatalf("source config mutated:\n%s", sourceConfig)
	}
	sourceCredentials, err := os.ReadFile(filepath.Join(userHome, "credentials", "kimi-code.json"))
	if err != nil {
		t.Fatalf("read source credentials: %v", err)
	}
	if string(sourceCredentials) != string(userCredentials) {
		t.Fatalf("source credentials mutated: %s", sourceCredentials)
	}
}

func TestGetAgentHooksSeedsAOManagedCredentialsFromUserKimiHome(t *testing.T) {
	workspace := t.TempDir()
	userHome := t.TempDir()
	aoHome := t.TempDir()
	t.Setenv(kimiCodeHomeEnv, userHome)
	userCredentials := []byte(`{"access_token":"user-token","refresh_token":"refresh-token"}`)
	userCredentialsPath := filepath.Join(userHome, "credentials", "kimi-code.json")
	if err := os.MkdirAll(filepath.Dir(userCredentialsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userCredentialsPath, userCredentials, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := (&Plugin{}).GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{
		WorkspacePath: workspace,
		Env:           map[string]string{kimiCodeHomeEnv: aoHome},
	}); err != nil {
		t.Fatalf("GetAgentHooks err = %v", err)
	}

	targetPath := filepath.Join(aoHome, "credentials", "kimi-code.json")
	got, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read AO credentials: %v", err)
	}
	if string(got) != string(userCredentials) {
		t.Fatalf("AO credentials = %s, want %s", got, userCredentials)
	}
	info, err := os.Stat(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Fatalf("AO credentials permissions = %o, want %o", got, want)
	}
}

// Kimi renews its 15-minute tokens with a rotating refresh token, so a copy
// seeded once went stale and could not sign in. AO's Kimi shares the user's.
func TestGetAgentHooksSharesTheUsersKimiLoginInPlaceOfAStaleCopy(t *testing.T) {
	workspace := t.TempDir()
	userHome := t.TempDir()
	aoHome := t.TempDir()
	t.Setenv(kimiCodeHomeEnv, userHome)
	for path, data := range map[string][]byte{
		filepath.Join(userHome, "credentials", "kimi-code.json"): []byte(`{"access_token":"user-token"}`),
		filepath.Join(aoHome, "credentials", "kimi-code.json"):   []byte(`{"access_token":"ao-token"}`),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := (&Plugin{}).GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{
		WorkspacePath: workspace,
		Env:           map[string]string{kimiCodeHomeEnv: aoHome},
	}); err != nil {
		t.Fatalf("GetAgentHooks err = %v", err)
	}

	targetPath := filepath.Join(aoHome, "credentials", "kimi-code.json")
	got, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read AO credentials: %v", err)
	}
	if want := `{"access_token":"user-token"}`; string(got) != want {
		t.Fatalf("AO credentials = %s, want %s", got, want)
	}
	assertKimiCredentialsShared(t, userHome, aoHome)
	// A token Kimi renews in AO's home is the user's too.
	if err := os.WriteFile(targetPath, []byte(`{"access_token":"renewed"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if mine, _ := os.ReadFile(filepath.Join(userHome, "credentials", "kimi-code.json")); string(mine) != `{"access_token":"renewed"}` {
		t.Fatalf("user credentials = %s, want the renewed token", mine)
	}
}

// Where no link can be made, the copy follows a newer login.
func TestSeedKimiCredentialReplacesAnOlderCopy(t *testing.T) {
	dir := t.TempDir()
	source, target := filepath.Join(dir, "user.json"), filepath.Join(dir, "ao", "kimi-code.json")
	for path, data := range map[string]string{source: `{"access_token":"new"}`, target: `{"access_token":"old"}`} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	older := time.Now().Add(-time.Hour)
	if err := os.Chtimes(target, older, older); err != nil {
		t.Fatal(err)
	}
	if err := seedKimiCredential(source, target); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != `{"access_token":"new"}` {
		t.Fatalf("copy = %s, want the newer login", got)
	}
	// A copy newer than the login, as one Kimi renewed itself, is kept.
	if err := os.WriteFile(target, []byte(`{"access_token":"renewed"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(source, older, older); err != nil {
		t.Fatal(err)
	}
	if err := seedKimiCredential(source, target); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != `{"access_token":"renewed"}` {
		t.Fatalf("copy = %s, want the renewed one kept", got)
	}
}

func assertKimiCredentialsShared(t *testing.T, userHome, aoHome string) {
	t.Helper()
	link, err := os.Readlink(filepath.Join(aoHome, "credentials"))
	if err != nil || !sameKimiConfigPath(link, filepath.Join(userHome, "credentials")) {
		t.Fatalf("AO credentials link = %q, %v; want the user's credentials", link, err)
	}
}

func TestGetAgentHooksReseedsAOManagedConfigWithoutAuth(t *testing.T) {
	workspace := t.TempDir()
	userHome := t.TempDir()
	aoHome := t.TempDir()
	t.Setenv(kimiCodeHomeEnv, userHome)
	if err := os.WriteFile(filepath.Join(userHome, "config.toml"), []byte(`api_key = "user-key"`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(aoHome, "config.toml"), []byte(kimiHooksConfigBlock()), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := (&Plugin{}).GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{
		WorkspacePath: workspace,
		Env:           map[string]string{kimiCodeHomeEnv: aoHome},
	}); err != nil {
		t.Fatalf("GetAgentHooks err = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(aoHome, "config.toml"))
	if err != nil {
		t.Fatalf("read AO config: %v", err)
	}
	text := string(data)
	for _, want := range []string{`api_key = "user-key"`, `command = "ao hooks kimi session-start"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("AO config missing %q:\n%s", want, text)
		}
	}
	if strings.Count(text, kimiHooksSentinelStart) != 1 {
		t.Fatalf("managed hook block duplicated:\n%s", text)
	}
}

func TestGetAgentHooksRewritesManagedKimiConfigBlock(t *testing.T) {
	workspace := t.TempDir()
	kimiHome := t.TempDir()
	configPath := filepath.Join(kimiHome, "config.toml")
	existing := "before = true\n\n" +
		kimiHooksSentinelStart + "\nold = true\n" + kimiHooksSentinelEnd + "\n\n" +
		"after = true\n"
	if err := os.WriteFile(configPath, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	plugin := &Plugin{}
	cfg := ports.WorkspaceHookConfig{
		WorkspacePath: workspace,
		Env:           map[string]string{"KIMI_CODE_HOME": kimiHome},
	}
	if err := plugin.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatalf("GetAgentHooks err = %v", err)
	}
	if err := plugin.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatalf("second GetAgentHooks err = %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	text := string(data)
	for _, want := range []string{"before = true", "after = true", `command = "ao hooks kimi session-start"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("config missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "old = true") {
		t.Fatalf("stale managed block preserved:\n%s", text)
	}
	if strings.Count(text, kimiHooksSentinelStart) != 1 {
		t.Fatalf("managed block duplicated:\n%s", text)
	}
}

func TestAugmentRuntimeEnvUsesAODataDir(t *testing.T) {
	env := map[string]string{kimiCodeHomeEnv: "/outside-ao"}
	dataDir := filepath.Join(t.TempDir(), "ao")

	(&Plugin{}).AugmentRuntimeEnv(env, dataDir)

	if got, want := env[kimiCodeHomeEnv], kimiCodeHomeDir(dataDir); got != want {
		t.Fatalf("%s = %q, want %q", kimiCodeHomeEnv, got, want)
	}
}

func TestGetAgentHooksRequiresAOManagedKimiHome(t *testing.T) {
	t.Setenv(kimiCodeHomeEnv, t.TempDir())

	err := (&Plugin{}).GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{
		WorkspacePath: t.TempDir(),
	})

	if err == nil || !strings.Contains(err.Error(), "AO-managed Kimi Code home is unavailable") {
		t.Fatalf("GetAgentHooks err = %v, want AO-managed Kimi home requirement", err)
	}
}

func TestGetAgentHooksReadsSystemPromptFile(t *testing.T) {
	workspace := t.TempDir()
	kimiHome := t.TempDir()
	promptFile := filepath.Join(t.TempDir(), "system.md")
	if err := os.WriteFile(promptFile, []byte("file rules\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := (&Plugin{}).GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{
		WorkspacePath:    workspace,
		SystemPromptFile: promptFile,
		Env:              map[string]string{"KIMI_CODE_HOME": kimiHome},
	}); err != nil {
		t.Fatalf("GetAgentHooks err = %v", err)
	}

	data, err := os.ReadFile(kimiInstructionsPath(workspace))
	if err != nil {
		t.Fatalf("read instructions: %v", err)
	}
	if !strings.Contains(string(data), "file rules") {
		t.Fatalf("instructions missing file rules:\n%s", data)
	}
}

func TestGetAgentHooksPreservesUserInstructions(t *testing.T) {
	workspace := t.TempDir()
	kimiHome := t.TempDir()
	path := kimiInstructionsPath(workspace)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("user instructions\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := (&Plugin{}).GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{
		WorkspacePath: workspace,
		SystemPrompt:  "AO rules",
		Env:           map[string]string{"KIMI_CODE_HOME": kimiHome},
	}); err != nil {
		t.Fatalf("GetAgentHooks err = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read instructions: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"user instructions",
		kimiInstructionsSentinel,
		"AO rules",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("instructions missing %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "user instructions") > strings.Index(text, kimiInstructionsSentinel) {
		t.Fatalf("user instructions should stay before AO-managed block:\n%s", text)
	}
}

func TestGetAgentHooksRewritesManagedInstructions(t *testing.T) {
	workspace := t.TempDir()
	kimiHome := t.TempDir()
	path := kimiInstructionsPath(workspace)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(kimiInstructionsSentinel+"\n\nold\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := (&Plugin{}).GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{
		WorkspacePath: workspace,
		SystemPrompt:  "new rules",
		Env:           map[string]string{"KIMI_CODE_HOME": kimiHome},
	}); err != nil {
		t.Fatalf("GetAgentHooks err = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read instructions: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "new rules") || strings.Contains(text, "old") {
		t.Fatalf("managed instructions not rewritten cleanly:\n%s", text)
	}
}

func TestGetAgentHooksRewritesManagedBlockAndPreservesSurroundingUserInstructions(t *testing.T) {
	workspace := t.TempDir()
	kimiHome := t.TempDir()
	path := kimiInstructionsPath(workspace)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	existing := "before\n\n" + kimiInstructionFile("old rules") + "\nafter\n"
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := (&Plugin{}).GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{
		WorkspacePath: workspace,
		SystemPrompt:  "new rules",
		Env:           map[string]string{"KIMI_CODE_HOME": kimiHome},
	}); err != nil {
		t.Fatalf("GetAgentHooks err = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read instructions: %v", err)
	}
	text := string(data)
	for _, want := range []string{"before", "after", "new rules"} {
		if !strings.Contains(text, want) {
			t.Fatalf("instructions missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "old rules") {
		t.Fatalf("stale managed instructions preserved:\n%s", text)
	}
	if strings.Count(text, kimiInstructionsSentinel) != 1 {
		t.Fatalf("managed block duplicated:\n%s", text)
	}
}

func TestSessionInfoNoOp(t *testing.T) {
	info, ok, err := (&Plugin{}).SessionInfo(context.Background(), ports.SessionRef{
		Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "01HZABC"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("ok=true with info %#v, want no-op false", info)
	}
	if !reflect.DeepEqual(info, ports.SessionInfo{}) {
		t.Fatalf("info = %#v, want zero", info)
	}
}

func TestContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := (&Plugin{}).GetConfigSpec(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetConfigSpec err = %v, want context.Canceled", err)
	}
	if _, err := (&Plugin{}).GetPromptDeliveryStrategy(ctx, ports.LaunchConfig{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetPromptDeliveryStrategy err = %v, want context.Canceled", err)
	}
	if err := (&Plugin{}).GetAgentHooks(ctx, ports.WorkspaceHookConfig{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetAgentHooks err = %v, want context.Canceled", err)
	}
	if _, _, err := (&Plugin{}).GetRestoreCommand(ctx, ports.RestoreConfig{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetRestoreCommand err = %v, want context.Canceled", err)
	}
	if _, _, err := (&Plugin{}).SessionInfo(ctx, ports.SessionRef{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("SessionInfo err = %v, want context.Canceled", err)
	}
	if _, err := ResolveKimiBinary(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ResolveKimiBinary err = %v, want context.Canceled", err)
	}
}

// Kimi Code reads <project>/.kimi-code/AGENTS.md, not the older .kimi/ one, so
// AO's instructions and a switch's handoff note go to both.
func TestPrepareACPInstructionsWritesWhereKimiCodeReads(t *testing.T) {
	workspace := t.TempDir()
	if err := PrepareACPInstructions(context.Background(), workspace, "AO standing instructions"); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{".kimi-code", ".kimi"} {
		data, err := os.ReadFile(filepath.Join(workspace, dir, "AGENTS.md"))
		if err != nil || !strings.Contains(string(data), "AO standing instructions") {
			t.Fatalf("%s/AGENTS.md = %q, %v; want AO's instructions", dir, data, err)
		}
		if _, err := os.Stat(filepath.Join(workspace, dir, ".gitignore")); err != nil {
			t.Fatalf("%s is not gitignored: %v", dir, err)
		}
	}
}

func TestPrepareACPInstructionsDoesNotWriteThroughALinkedFolder(t *testing.T) {
	workspace, elsewhere := t.TempDir(), t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(workspace, ".kimi-code")); err != nil {
		t.Fatal(err)
	}
	if err := PrepareACPInstructions(context.Background(), workspace, "AO standing instructions"); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Fatalf("wrote %d files outside the workspace through the link", len(entries))
	}
	if !CodeInstructionsOwnedByProject(context.Background(), workspace) {
		t.Fatal("a linked .kimi-code was not left to the project, so the chat would drop AO's instructions")
	}
}
