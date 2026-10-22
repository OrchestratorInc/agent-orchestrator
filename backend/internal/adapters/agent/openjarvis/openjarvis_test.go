package openjarvis

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestInputFailsClosed(t *testing.T) {
	p := New()
	for _, message := range []string{"", "one\ntwo", "one\rtwo", "yes\x03", "\x1b[200~yes", "/clear", strings.Repeat("é", 1501), " leading", "trailing ", "Explain You>", "Confirm: yes"} {
		if err := p.ValidateTerminalMessage(message, "You> "); !errors.Is(err, ports.ErrAgentInputRejected) {
			t.Fatalf("accepted %q", message)
		}
	}
	for _, screen := range []string{"", "You> working", "You> Explain the marker You>\n", "You> draft ending You>", "You> old\nConfirm: Allow execution of tool 'shell_exec'? [y/N]", "Generation interrupted.", "Agent 'orchestrator' failed: missing tools"} {
		if err := p.ValidateTerminalMessage("yes", screen); !errors.Is(err, ports.ErrAgentInputRejected) {
			t.Fatalf("accepted current screen %q", screen)
		}
	}
	if err := p.ValidateTerminalMessage("-A plain Unicode task é", "old output\nYou> "); err != nil {
		t.Fatal(err)
	}
}
func TestOverlayPreservesSecurityPersonaAndPrefix(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, "config.toml")
	original := []byte("[tools]\nenabled=['file_read','file_write','shell_exec','code_interpreter']\n[engine]\ndefault='cloud'\n[agent]\ndefault_agent='orchestrator'\n[security]\nenabled=true\n[system_prompt]\nprefix='Native prefix'\n[memory_files]\nsoul_path='relative/SOUL.md'\n")
	if err := os.WriteFile(source, original, 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"OPENJARVIS_HOME": home}
	workspace := t.TempDir()
	if err := New().GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "ao-1", Env: env, WorkspacePath: workspace, SystemPrompt: "AO standing"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(env["OPENJARVIS_CONFIG"])
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := toml.Unmarshal(b, &config); err != nil {
		t.Fatal(err)
	}
	prefix := config["system_prompt"].(map[string]any)["prefix"].(string)
	if !strings.HasPrefix(prefix, "Native prefix\n\nAO standing\n\n") || !strings.Contains(prefix, `env_passthrough=["AO_SESSION_ID","AO_RUN_FILE","AO_PROJECT_ID","AO_DATA_DIR","AO_RUNTIME_LAUNCH_ID"]`) {
		t.Fatal("lost additive prefix")
	}
	if !strings.Contains(prefix, workspace) {
		t.Fatal("missing actual session workspace")
	}
	if config["security"].(map[string]any)["enabled"] != true || config["memory_files"].(map[string]any)["soul_path"] != "relative/SOUL.md" {
		t.Fatal("lost native settings")
	}
	b, err = os.ReadFile(source)
	if err != nil || string(b) != string(original) {
		t.Fatal("changed original config")
	}
}
func TestRestoreAlwaysRefused(t *testing.T) {
	_, ok, err := New().GetRestoreCommand(context.Background(), ports.RestoreConfig{})
	if ok || !errors.Is(err, ports.ErrAgentRestoreUnsupported) {
		t.Fatal("native restore fabricated")
	}
}

// OpenJarvis has no approval flags, so every AO mode launches with native
// security, as other TUI harnesses without per-launch flags do.
func TestEveryPermissionModeUsesNativeDefaults(t *testing.T) {
	for _, mode := range []ports.PermissionMode{ports.PermissionModeDefault, ports.PermissionModeAcceptEdits, ports.PermissionModeAuto, ports.PermissionModeBypassPermissions} {
		if _, err := New().GetLaunchCommand(context.Background(), ports.LaunchConfig{Permissions: mode}); errors.Is(err, ports.ErrAgentPermissionModeUnsupported) {
			t.Fatalf("mode %s refused: %v", mode, err)
		}
	}
}

func TestEchoRequiresNewCurrentSubmission(t *testing.T) {
	p := New()
	old := "You> Check status\nDone\nYou> "
	if p.TerminalMessageObserved("Check status", old, old) {
		t.Fatal("historical echo admitted send")
	}
	if p.TerminalMessageObserved("Check status", old, "You> Check status\nDone\nYou> Other") {
		t.Fatal("unrelated output admitted send")
	}
	if !p.TerminalMessageObserved("Check status", old, old+"Check status") {
		t.Fatal("new current echo not witnessed")
	}
}
func readOverlay(t *testing.T, dataDir string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dataDir, "agents", "openjarvis", "test", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := toml.Unmarshal(b, &config); err != nil {
		t.Fatal(err)
	}
	return config
}

// A fresh install has no config and the PyPI package has no code-assistant
// preset; AO supplies the coding tools and, with a provider key, the cloud
// engine and a working model.
func TestFreshInstallGetsCodeAssistantDefaults(t *testing.T) {
	dataDir := t.TempDir()
	env := map[string]string{"OPENJARVIS_HOME": t.TempDir(), "GEMINI_API_KEY": "key"}
	for _, unset := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GOOGLE_API_KEY", "OPENROUTER_API_KEY", "MINIMAX_API_KEY", "DEEPSEEK_API_KEY"} {
		env[unset] = ""
	}
	if err := New().GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{DataDir: dataDir, SessionID: "test", Env: env}); err != nil {
		t.Fatal(err)
	}
	config := readOverlay(t, dataDir)
	tools := fmt.Sprint(config["tools"].(map[string]any)["enabled"])
	for _, tool := range codeAssistantTools {
		if !strings.Contains(tools, tool) {
			t.Fatalf("tool %s missing: %s", tool, tools)
		}
	}
	if config["engine"].(map[string]any)["default"] != "cloud" || config["intelligence"].(map[string]any)["default_model"] != "gemini-3-flash-preview" {
		t.Fatalf("cloud defaults missing: %v", config)
	}
}

func TestUserEngineAndModelArePreserved(t *testing.T) {
	home, dataDir := t.TempDir(), t.TempDir()
	native := "[engine]\ndefault = 'cloud'\n[intelligence]\ndefault_model = 'my-model'\n[tools]\nenabled = ['web_search']\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(native), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"OPENJARVIS_HOME": home, "GEMINI_API_KEY": "key"}
	if err := New().GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{DataDir: dataDir, SessionID: "test", Env: env}); err != nil {
		t.Fatal(err)
	}
	config := readOverlay(t, dataDir)
	if config["intelligence"].(map[string]any)["default_model"] != "my-model" || !strings.Contains(fmt.Sprint(config["tools"].(map[string]any)["enabled"]), "web_search") {
		t.Fatalf("user settings lost: %v", config)
	}
}

func TestWrappedExactEcho(t *testing.T) {
	p := New()
	if !p.TerminalMessageObserved("Read proof.json and update count", "You> ", "You> Read proof.json and\n update count") {
		t.Fatal("physical wrap rejected")
	}
	if p.TerminalMessageObserved("Read proof.json and update count", "You> ", "You> Read proof.json and\n different count") {
		t.Fatal("changed content witnessed")
	}
	if p.ValidateTerminalMessage("task\u0085You>", "You> ") == nil {
		t.Fatal("C1 control accepted")
	}
}

// isolateAuth clears every provider key, points the native config at an empty
// home and records which key AuthStatus probes.
func isolateAuth(t *testing.T, model string) *string {
	t.Helper()
	for _, provider := range cloudProviderKeys {
		t.Setenv(provider.env, "")
	}
	t.Setenv("OPENAI_CODEX_API_KEY", "")
	t.Setenv("OPENAI_BASE_URL", "")
	home := t.TempDir()
	t.Setenv("OPENJARVIS_HOME", home)
	t.Setenv("OPENJARVIS_CONFIG", "")
	if model != "" {
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(fmt.Sprintf("[intelligence]\ndefault_model = %q\n", model)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	probed := new(string)
	previous := providerProbe
	providerProbe = func(_ context.Context, keyEnv, _ string) ports.AgentAuthStatus {
		*probed = keyEnv
		return ports.AgentAuthStatusAuthorized
	}
	t.Cleanup(func() { providerProbe = previous })
	return probed
}

func TestAnyCloudProviderKeyIsProbedWithoutRoutedModel(t *testing.T) {
	for _, provider := range cloudProviderKeys {
		t.Run(provider.env, func(t *testing.T) {
			probed := isolateAuth(t, "")
			t.Setenv(provider.env, "key")
			if got, err := New().AuthStatus(context.Background()); err != nil || got != ports.AgentAuthStatusAuthorized || *probed != provider.env {
				t.Fatalf("status = %q, %v, probed %q", got, err, *probed)
			}
		})
	}
}

func TestAuthStatusProbesTheDefaultModelsProvider(t *testing.T) {
	probed := isolateAuth(t, "MiniMax-M3")
	t.Setenv("OPENAI_API_KEY", "openai")
	t.Setenv("MINIMAX_API_KEY", "minimax")
	if got, err := New().AuthStatus(context.Background()); err != nil || got != ports.AgentAuthStatusAuthorized || *probed != "MINIMAX_API_KEY" {
		t.Fatalf("status = %q, %v, probed %q", got, err, *probed)
	}
}

func TestAuthStatusMissingKeyForDefaultModelIsUnauthorized(t *testing.T) {
	probed := isolateAuth(t, "deepseek-v4-flash")
	t.Setenv("OPENAI_API_KEY", "openai")
	if got, err := New().AuthStatus(context.Background()); err != nil || got != ports.AgentAuthStatusUnauthorized || *probed != "" {
		t.Fatalf("status = %q, %v, probed %q", got, err, *probed)
	}
}

func TestAuthStatusWithoutKeysIsUnknown(t *testing.T) {
	isolateAuth(t, "qwen3.5:2b")
	if got, err := New().AuthStatus(context.Background()); err != nil || got != ports.AgentAuthStatusUnknown {
		t.Fatalf("status = %q, %v", got, err)
	}
}

func TestProbeProviderKeyMapsProviderResponses(t *testing.T) {
	for code, want := range map[int]ports.AgentAuthStatus{
		http.StatusOK:                  ports.AgentAuthStatusAuthorized,
		http.StatusUnauthorized:        ports.AgentAuthStatusUnauthorized,
		http.StatusForbidden:           ports.AgentAuthStatusUnauthorized,
		http.StatusTooManyRequests:     ports.AgentAuthStatusConfigured,
		http.StatusInternalServerError: ports.AgentAuthStatusConfigured,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer secret" {
				t.Errorf("authorization = %q", r.Header.Get("Authorization"))
			}
			w.WriteHeader(code)
		}))
		previous := providerModelsEndpoints["MINIMAX_API_KEY"]
		providerModelsEndpoints["MINIMAX_API_KEY"] = server.URL + "/v1/models"
		got := probeProviderKey(context.Background(), "MINIMAX_API_KEY", "secret")
		providerModelsEndpoints["MINIMAX_API_KEY"] = previous
		server.Close()
		if got != want {
			t.Fatalf("HTTP %d: status = %q, want %q", code, got, want)
		}
	}
}

func TestProbeProviderKeyFollowsOpenAIBaseURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/compat/models" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	t.Setenv("OPENAI_BASE_URL", server.URL+"/compat/")
	if got := probeProviderKey(context.Background(), "OPENAI_API_KEY", "secret"); got != ports.AgentAuthStatusUnauthorized {
		t.Fatalf("status = %q", got)
	}
}

func TestProbeProviderKeyOfflineStaysConfigured(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()
	previous := providerModelsEndpoints["DEEPSEEK_API_KEY"]
	providerModelsEndpoints["DEEPSEEK_API_KEY"] = server.URL + "/models"
	t.Cleanup(func() { providerModelsEndpoints["DEEPSEEK_API_KEY"] = previous })
	if got := probeProviderKey(context.Background(), "DEEPSEEK_API_KEY", "secret"); got != ports.AgentAuthStatusConfigured {
		t.Fatalf("status = %q", got)
	}
	if got := probeProviderKey(context.Background(), "OPENAI_CODEX_API_KEY", "secret"); got != ports.AgentAuthStatusConfigured {
		t.Fatalf("codex status = %q", got)
	}
}
