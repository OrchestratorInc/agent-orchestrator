package claudeacp

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestClaudeACPLaunchEnvPreservesExecutableAndProjectEnvironment(t *testing.T) {
	env := claudeACPLaunchEnv(map[string]string{"PROJECT_ENV": "kept"}, "/opt/claude", "")
	if env["CLAUDE_CODE_EXECUTABLE"] != "/opt/claude" || env["PROJECT_ENV"] != "kept" {
		t.Fatalf("env=%v", env)
	}
	if env["CLAUDE_MODEL_CONFIG"] != "" {
		t.Fatal("native model list was overridden")
	}
}

func TestClaudeACPLaunchEnvPreservesExplicitModelConfiguration(t *testing.T) {
	existing := `{"availableModels":["team-opus"],"modelOverrides":{"team-opus":"arn:team:opus"}}`
	env := claudeACPLaunchEnv(
		map[string]string{"CLAUDE_MODEL_CONFIG": existing},
		"/opt/claude",
		"",
	)
	if env["CLAUDE_MODEL_CONFIG"] != existing {
		t.Fatalf("CLAUDE_MODEL_CONFIG = %q, want explicit user configuration preserved", env["CLAUDE_MODEL_CONFIG"])
	}
}

func TestClaudeACPLaunchEnvPreservesModelOverridesWithoutRestrictingNativePicker(t *testing.T) {
	existing := `{"modelOverrides":{"custom":"arn:team:opus"}}`
	env := claudeACPLaunchEnv(map[string]string{"CLAUDE_MODEL_CONFIG": existing}, "/opt/claude", "")
	if env["CLAUDE_MODEL_CONFIG"] != existing {
		t.Fatal("user model overrides changed")
	}
}

func TestClaudeACPLaunchEnvPreservesInvalidModelConfiguration(t *testing.T) {
	for _, value := range []string{"null", "{"} {
		t.Run(value, func(t *testing.T) {
			env := claudeACPLaunchEnv(
				map[string]string{"CLAUDE_MODEL_CONFIG": value},
				"/opt/claude",
				"",
			)
			if env["CLAUDE_MODEL_CONFIG"] != value {
				t.Fatalf("CLAUDE_MODEL_CONFIG = %q, want invalid user value %q preserved for ACP to report", env["CLAUDE_MODEL_CONFIG"], value)
			}
		})
	}
}

func TestClaudeACPLaunchEnvAddsCustomSelectionWithoutRestrictingNativePicker(t *testing.T) {
	t.Setenv("ANTHROPIC_CUSTOM_MODEL_OPTION", "")
	env := claudeACPLaunchEnv(nil, "/opt/claude", "custom-model")
	if env["ANTHROPIC_CUSTOM_MODEL_OPTION"] != "custom-model" || env["CLAUDE_MODEL_CONFIG"] != "" {
		t.Fatalf("env=%v", env)
	}
}

func TestClaudeACPLaunchEnvPreservesNativePickerWithoutProviderCatalog(t *testing.T) {
	t.Setenv("CLAUDE_MODEL_CONFIG", "")
	env := claudeACPLaunchEnv(nil, "/opt/claude", "sonnet")
	if got := env["CLAUDE_MODEL_CONFIG"]; got != "" {
		t.Fatalf("CLAUDE_MODEL_CONFIG = %q, want native ACP picker preserved", got)
	}
}

func TestClaudeACPLaunchEnvAddsSelectedProviderModelAsCustomOption(t *testing.T) {
	t.Setenv("CLAUDE_MODEL_CONFIG", "")
	t.Setenv("ANTHROPIC_CUSTOM_MODEL_OPTION", "")
	env := claudeACPLaunchEnv(
		nil,
		"/opt/claude",
		"claude-fable-5",
	)
	if got := env["ANTHROPIC_CUSTOM_MODEL_OPTION"]; got != "claude-fable-5" {
		t.Fatalf("ANTHROPIC_CUSTOM_MODEL_OPTION = %q, want selected provider model", got)
	}
}

func TestClaudeACPLaunchEnvDoesNotAddNativeAliasesAsCustomOptions(t *testing.T) {
	t.Setenv("CLAUDE_MODEL_CONFIG", "")
	t.Setenv("ANTHROPIC_CUSTOM_MODEL_OPTION", "")
	for _, alias := range []string{"default", "sonnet", "opus", "haiku", "fable", "opus[1m]"} {
		t.Run(alias, func(t *testing.T) {
			env := claudeACPLaunchEnv(nil, "/opt/claude", alias)
			if got := env["ANTHROPIC_CUSTOM_MODEL_OPTION"]; got != "" {
				t.Fatalf("ANTHROPIC_CUSTOM_MODEL_OPTION = %q, want native alias omitted", got)
			}
		})
	}
}

func TestClaudeACPLaunchEnvPreservesExplicitCustomModelOption(t *testing.T) {
	t.Setenv("ANTHROPIC_CUSTOM_MODEL_OPTION", "")
	env := claudeACPLaunchEnv(
		map[string]string{"ANTHROPIC_CUSTOM_MODEL_OPTION": "team-model"},
		"/opt/claude",
		"claude-fable-5",
	)
	if got := env["ANTHROPIC_CUSTOM_MODEL_OPTION"]; got != "team-model" {
		t.Fatalf("ANTHROPIC_CUSTOM_MODEL_OPTION = %q, want explicit user value preserved", got)
	}
}

func TestClaudeSessionMetaAppendsWithoutReplacingPreset(t *testing.T) {
	if got := claudeSessionMeta(acpdriver.LaunchConfig{}); got != nil {
		t.Fatalf("empty prompt metadata = %#v", got)
	}
	meta := claudeSessionMeta(acpdriver.LaunchConfig{SystemPrompt: "AO standing instructions"})
	prompt, ok := meta["systemPrompt"].(map[string]any)
	if !ok {
		t.Fatalf("systemPrompt = %#v", meta["systemPrompt"])
	}
	if prompt["type"] != "preset" || prompt["preset"] != "claude_code" || prompt["append"] != "AO standing instructions" {
		t.Fatalf("systemPrompt = %#v", prompt)
	}
}

func TestClaudeSessionMetaNeverIncludesReplayContext(t *testing.T) {
	meta := claudeSessionMeta(acpdriver.LaunchConfig{SystemPrompt: "AO standing instructions"})
	prompt := meta["systemPrompt"].(map[string]any)
	if strings.Contains(prompt["append"].(string), "replayed-conversation") {
		t.Fatal("replay context entered the system prompt")
	}
}

func TestClaudeSessionModeUsesAdapterModeIDs(t *testing.T) {
	tests := map[ports.PermissionMode]string{
		ports.PermissionModeDefault:           "",
		ports.PermissionModeAcceptEdits:       "acceptEdits",
		ports.PermissionModeAuto:              "auto",
		ports.PermissionModeBypassPermissions: "bypassPermissions",
	}
	for permission, want := range tests {
		if got := claudeSessionMode(permission); got != want {
			t.Errorf("mode(%q) = %q, want %q", permission, got, want)
		}
	}
}

func TestClaudeSessionOptionsUseACPConfigIDs(t *testing.T) {
	got := claudeSessionOptions(ports.ChatTurnSettings{Model: "sonnet", Effort: "high"})
	want := []acpdriver.SessionOption{{ID: "model", Value: "sonnet"}, {ID: "effort", Value: "high"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("options = %#v, want %#v", got, want)
	}
}

func TestValidateClaudeACPExecutableRejectsWindowsCommandShims(t *testing.T) {
	tests := []struct {
		name    string
		binary  string
		goos    string
		wantErr bool
	}{
		{name: "native executable", binary: `C:\\npm\\claude.exe`, goos: "windows"},
		{name: "cmd shim", binary: `C:\\npm\\claude.cmd`, goos: "windows", wantErr: true},
		{name: "bat shim", binary: `C:\\npm\\claude.BAT`, goos: "windows", wantErr: true},
		{name: "non-Windows shim", binary: "/tmp/claude.cmd", goos: "linux"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateClaudeACPExecutable(tc.binary, tc.goos)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateClaudeACPExecutable(%q, %q) error = %v, wantErr %v", tc.binary, tc.goos, err, tc.wantErr)
			}
			if tc.wantErr && !strings.Contains(err.Error(), "native claude.exe") {
				t.Fatalf("error = %q, want actionable native executable guidance", err)
			}
		})
	}
}

func TestRuntimeCommandOverride(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AO_CLAUDE_ACP_COMMAND", executable)
	launch, err := resolveRuntime(context.Background())
	if err != nil {
		t.Fatalf("resolveRuntime: %v", err)
	}
	if launch.command != executable || len(launch.args) != 0 {
		t.Fatalf("runtime = %#v", launch)
	}
}

type rejectedClaudePlugin struct {
	binary     string
	workingDir string
	env        map[string]string
}

func (p rejectedClaudePlugin) ResolveBinary(context.Context) (string, error) { return p.binary, nil }
func (p *rejectedClaudePlugin) ValidateLaunchAuth(_ context.Context, workingDir string, env map[string]string) (ports.AgentAuthStatus, error) {
	p.workingDir = workingDir
	p.env = env
	return ports.AgentAuthStatusUnauthorized, nil
}

func TestCapabilityProbeDoesNotUseDaemonGlobalClaudeAuth(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AO_CLAUDE_ACP_COMMAND", executable)
	_, err = New(&rejectedClaudePlugin{binary: executable}, nil, nil).Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe error = %v, want capability probe independent of global auth", err)
	}
}

func TestLaunchAuthBlocksAConfirmedProjectCredentialRejection(t *testing.T) {
	plugin := &rejectedClaudePlugin{binary: "/tmp/claude"}
	env := map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1", "AWS_PROFILE": "project-profile"}
	err := validateClaudeLaunchAuth(
		context.Background(), plugin, "/work/project", env, nil,
	)
	if !errors.Is(err, ports.ErrAgentAuthRequired) {
		t.Fatalf("launch auth error = %v, want ErrAgentAuthRequired", err)
	}
	if plugin.workingDir != "/work/project" || !reflect.DeepEqual(plugin.env, env) {
		t.Fatalf("launch auth context = dir %q env %#v, want project cwd and merged launch env", plugin.workingDir, plugin.env)
	}
}

func TestClaudePromptResponseFailureRequiresStructuredLoginAction(t *testing.T) {
	response := acpsdk.PromptResponse{
		StopReason: acpsdk.StopReasonEndTurn,
		Meta: map[string]any{"jetbrains": map[string]any{"air": map[string]any{
			"version": float64(1),
			"sessionFailure": map[string]any{
				"id": "auth-expired", "severity": "error",
				"title":   "Your login has expired.",
				"details": "Run /login to sign in again.",
				"actions": []any{"login"},
			},
		}}},
	}

	err := claudePromptResponseFailure(response)
	if !errors.Is(err, ports.ErrChatAuthRequired) {
		t.Fatalf("error = %v, want ErrChatAuthRequired", err)
	}
}

func TestClaudePromptResponseFailureIgnoresAssistantAuthProse(t *testing.T) {
	response := acpsdk.PromptResponse{
		StopReason: acpsdk.StopReasonEndTurn,
		Meta:       map[string]any{"assistantMessage": "Your OAuth token has expired; please run /login."},
	}
	if err := claudePromptResponseFailure(response); err != nil {
		t.Fatalf("assistant prose classified as terminal auth failure: %v", err)
	}
}

func TestClaudeAuthRejectionNotifiesDaemon(t *testing.T) {
	called := 0
	callback := claudeAuthRejected(func() { called++ })
	callback()
	if called != 1 {
		t.Fatalf("daemon callback calls = %d, want 1", called)
	}
}
