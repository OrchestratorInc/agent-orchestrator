package codebuddy

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
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// This fake native CLI exercises the subprocess boundary without opening any
// real native credential store or making provider network requests.
func fakeAuthCLI(t *testing.T, output string, exitCode string) *Plugin {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake executable")
	}
	t.Setenv("AO_DATA_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "codebuddy")
	script := "#!/bin/sh\ncat <<'AO_NATIVE_RESULT'\n" + output + "\nAO_NATIVE_RESULT\nexit " + exitCode + "\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return &Plugin{binary: path}
}

func TestAuthVerifiesNativeSelectedProvider(t *testing.T) {
	t.Setenv("CODEBUDDY_API_KEY", "")
	t.Setenv("CODEBUDDY_AUTH_TOKEN", "")
	p := fakeAuthCLI(t, `[
 {"type":"system","subtype":"init","model":"native-custom-model"},
 {"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"OK"}]}},
 {"type":"result","subtype":"success","is_error":false,"result":"OK","usage":{"input_tokens":10,"output_tokens":1}}
 ]`, "0")
	got, err := p.AuthStatus(context.Background())
	if err != nil || got != ports.AgentAuthStatusAuthorized {
		t.Fatalf("native provider success = %s, %v; want authorized", got, err)
	}
}

func TestAuthRejectsStructuredNativeCredentialFailure(t *testing.T) {
	t.Setenv("CODEBUDDY_API_KEY", "not-a-real-key")
	p := fakeAuthCLI(t, `{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["secret must stay private"],"errors_info":[{"status":401,"category":"auth"}]}`, "1")
	got, err := p.AuthStatus(context.Background())
	if err != nil || got != ports.AgentAuthStatusUnauthorized {
		t.Fatalf("provider rejection = %s, %v; want unauthorized", got, err)
	}
}

func TestAuthCancellationStopsNativeProbe(t *testing.T) {
	p := fakeAuthCLI(t, "", "0")
	if err := os.WriteFile(p.binary, []byte("#!/bin/sh\nsleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	got, err := p.AuthStatus(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || got == ports.AgentAuthStatusAuthorized {
		t.Fatalf("cancellation = %s, %v", got, err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("native child did not stop promptly")
	}
}

func TestNativeAuthResultRequiresCompletedInference(t *testing.T) {
	success := `{"type":"result","subtype":"success","is_error":false,"result":"OK","usage":{"output_tokens":1}}`
	cases := []struct {
		name, raw      string
		exitOK         bool
		fallback, want ports.AgentAuthStatus
		code           string
	}{
		{"native-array", "[" + success + "]", true, ports.AgentAuthStatusUnknown, ports.AgentAuthStatusAuthorized, "provider_accepted"},
		{"cancelled-success", strings.Replace(success, `"result":"OK"`, `"result":"OK","terminal_reason":"aborted_streaming"`, 1), true, ports.AgentAuthStatusConfigured, ports.AgentAuthStatusConfigured, "check_inconclusive"},
		{"empty-success", strings.Replace(success, `"result":"OK"`, `"result":""`, 1), true, ports.AgentAuthStatusUnknown, ports.AgentAuthStatusUnknown, "check_inconclusive"},
		{"no-inference-usage", strings.Replace(success, `"output_tokens":1`, `"output_tokens":0`, 1), true, ports.AgentAuthStatusUnknown, ports.AgentAuthStatusUnknown, "check_inconclusive"},
		{"missing-error-flag", strings.Replace(success, `"is_error":false,`, "", 1), true, ports.AgentAuthStatusUnknown, ports.AgentAuthStatusUnknown, "output_unrecognized"},
		{"failed-exit", success, false, ports.AgentAuthStatusConfigured, ports.AgentAuthStatusConfigured, "check_inconclusive"},
		{"prose", "Authenticated: true; API key found; secret", true, ports.AgentAuthStatusUnknown, ports.AgentAuthStatusUnknown, "output_unrecognized"},
		{"history-result-not-terminal", "[" + success + `,{"type":"assistant"}]`, true, ports.AgentAuthStatusUnknown, ports.AgentAuthStatusUnknown, "output_unrecognized"},
		{"structured-rejection", `{"type":"result","subtype":"error_during_execution","is_error":true,"errors_info":[{"status":403,"category":"auth","details":"secret"}]}`, false, ports.AgentAuthStatusConfigured, ports.AgentAuthStatusUnauthorized, "provider_rejected"},
		{"quota", `{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["unauthorized secret"],"errors_info":[{"status":429,"category":"quota"}]}`, false, ports.AgentAuthStatusConfigured, ports.AgentAuthStatusConfigured, "provider_quota"},
		{"quota-no-local-evidence", `{"type":"result","subtype":"error_during_execution","is_error":true,"errors_info":[{"status":429}]}`, false, ports.AgentAuthStatusUnknown, ports.AgentAuthStatusUnknown, "provider_quota"},
		{"category-only-auth", `{"type":"result","subtype":"error_during_execution","is_error":true,"errors_info":[{"category":"auth"}]}`, false, ports.AgentAuthStatusConfigured, ports.AgentAuthStatusConfigured, "check_inconclusive"},
		{"auth-category-with-validation-status", `{"type":"result","subtype":"error_during_execution","is_error":true,"errors_info":[{"status":400,"category":"auth"}]}`, false, ports.AgentAuthStatusUnknown, ports.AgentAuthStatusUnknown, "check_inconclusive"},
		{"transport", `{"type":"result","subtype":"error_during_execution","is_error":true,"errors_info":[{"category":"network"}]}`, false, ports.AgentAuthStatusConfigured, ports.AgentAuthStatusConfigured, "provider_unreachable"},
		{"unstructured-error", `{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["401 unauthorized secret"]}`, false, ports.AgentAuthStatusUnknown, ports.AgentAuthStatusUnknown, "check_inconclusive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, code := classifyAuthResult([]byte(tc.raw), tc.exitOK, tc.fallback)
			if got != tc.want || code != tc.code {
				t.Fatalf("got %s/%s, want %s/%s", got, code, tc.want, tc.code)
			}
		})
	}
}

func TestAuthDiagnosticsKeepNativeResponsesPrivate(t *testing.T) {
	t.Setenv("CODEBUDDY_API_KEY", "private-provider-key")
	p := fakeAuthCLI(t, `{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["private-provider-key https://private.invalid /private/profile"],"errors_info":[{"category":"quota","details":"private-provider-key"}]}`, "1")
	got, identity, steps, err := p.AuthStatusWithDiagnostics(context.Background())
	if err != nil || got != ports.AgentAuthStatusConfigured || len(steps) != 1 || steps[0].Code != "provider_quota" {
		t.Fatalf("verdict=%s steps=%+v err=%v", got, steps, err)
	}
	raw, _ := json.Marshal([]any{identity, steps})
	for _, private := range []string{"private-provider-key", "private.invalid", "/private/profile", p.binary} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("native private data escaped diagnostics: %s", private)
		}
	}
	if !strings.Contains(strings.ToLower(steps[0].Reason), "limit") {
		t.Fatalf("quota explanation not actionable: %s", steps[0].Reason)
	}
}

func TestAuthProbeUsesNativeUserProfileAndRestrictedCommand(t *testing.T) {
	home := t.TempDir()
	nativeProfile := filepath.Join(home, "native-profile")
	t.Setenv("HOME", home)
	t.Setenv("CODEBUDDY_CONFIG_DIR", nativeProfile)
	t.Setenv("CODEBUDDY_BASE_URL", "https://selected-provider.invalid/v1")
	t.Setenv("CODEBUDDY_MODEL", "selected-provider-model")
	t.Setenv("CODEBUDDY_API_KEY", "private-test-key")
	t.Setenv("ZAI_API_KEY", "unrelated-provider-key")
	p := fakeAuthCLI(t, `{"type":"result","subtype":"success","is_error":false,"result":"OK","usage":{"output_tokens":1}}`, "0")
	capture := filepath.Join(t.TempDir(), "command")
	script := "#!/bin/sh\nprintf '%s\\n' \"$PWD\" \"$HOME\" \"$CODEBUDDY_CONFIG_DIR\" \"$CODEBUDDY_BASE_URL\" \"$CODEBUDDY_MODEL\" \"$@\" > '" + capture + "'\nprintf '%s\\n' '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"OK\",\"usage\":{\"output_tokens\":1}}'\n"
	if err := os.WriteFile(p.binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	got, err := p.AuthStatus(context.Background())
	if err != nil || got != ports.AgentAuthStatusAuthorized {
		t.Fatalf("verdict=%s err=%v", got, err)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 5 {
		t.Fatalf("capture=%q", lines)
	}
	if filepath.Dir(lines[0]) != filepath.Join(os.Getenv("AO_DATA_DIR"), "auth-probes") || lines[1] != home || lines[2] != nativeProfile || lines[3] != "https://selected-provider.invalid/v1" || lines[4] != "selected-provider-model" {
		t.Fatalf("native launch context=%q", lines[:5])
	}
	if _, err := os.Stat(lines[0]); !os.IsNotExist(err) {
		t.Fatalf("probe workspace survived: %v", err)
	}
	args := lines[5:]
	want := []string{"--print", "--output-format", "json", "--tools", "", "--max-turns", "1", "--no-session-persistence", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--setting-sources", "user", "--settings", `{"disableAllHooks":true}`, "--agent", "cli", "--system-prompt", "Reply briefly without using tools.", "--", "Reply with OK."}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("unsafe native probe argv=%q", args)
	}
}

func TestAuthProbeBoundsOutput(t *testing.T) {
	t.Setenv("CODEBUDDY_API_KEY", "")
	t.Setenv("CODEBUDDY_AUTH_TOKEN", "")
	p := fakeAuthCLI(t, strings.Repeat("x", authProbeOutputLimit+1), "0")
	got, _, steps, err := p.AuthStatusWithDiagnostics(context.Background())
	if err != nil || got != ports.AgentAuthStatusUnknown || len(steps) != 1 || steps[0].Code != "output_unrecognized" {
		t.Fatalf("oversized result=%s/%+v err=%v", got, steps, err)
	}
}

func TestOtherProviderEnvironmentDoesNotConfigureCodeBuddy(t *testing.T) {
	t.Setenv("CODEBUDDY_API_KEY", "")
	t.Setenv("CODEBUDDY_AUTH_TOKEN", "")
	t.Setenv("ZAI_API_KEY", "not-a-codebuddy-route")
	t.Setenv("MINIMAX_API_KEY", "not-a-codebuddy-route")
	p := fakeAuthCLI(t, "native output unrecognized", "1")
	got, err := p.AuthStatus(context.Background())
	if err != nil || got != ports.AgentAuthStatusUnknown {
		t.Fatalf("unrelated provider environment=%s err=%v", got, err)
	}
}

func TestAuthProbeDisablesFunctionHookMods(t *testing.T) {
	t.Setenv("CBC_ENABLE_FUNCTION_HOOKS", "1")
	t.Setenv("CLAUDE_CODE_ENABLE_FUNCTION_HOOKS", "1")
	t.Setenv("CODEBUDDY_PLUGIN_DIRS", "/fixture/native-plugins")
	t.Setenv("CODEBUDDY_API_KEY", "fixture-provider-key")
	t.Setenv("CODEBUDDY_MODEL", "fixture-provider-model")
	marker := filepath.Join(t.TempDir(), "function-mod-ran")
	t.Setenv("AO_TEST_MOD_MARKER", marker)
	p := fakeAuthCLI(t, "", "0")
	script := `#!/bin/sh
if [ "$CBC_ENABLE_FUNCTION_HOOKS" != "0" ] || [ -n "$CODEBUDDY_PLUGIN_DIRS" ]; then
  : > "$AO_TEST_MOD_MARKER"
fi
if [ "$CODEBUDDY_API_KEY" != "fixture-provider-key" ] || [ "$CODEBUDDY_MODEL" != "fixture-provider-model" ]; then
  exit 4
fi
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"OK","usage":{"output_tokens":1}}'
`
	if err := os.WriteFile(p.binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if status, err := p.AuthStatus(t.Context()); err != nil || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("isolated provider check = %s, %v", status, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("function-hook mod could run during auth check: %v", err)
	}
	if os.Getenv("CBC_ENABLE_FUNCTION_HOOKS") != "1" || os.Getenv("CODEBUDDY_PLUGIN_DIRS") != "/fixture/native-plugins" {
		t.Fatal("probe changed the normal native session environment")
	}
}

func TestAuthProbeUsesCanonicalDataDirAfterDaemonChdir(t *testing.T) {
	p := fakeAuthCLI(t, "", "0")
	dataDir := filepath.Join(t.TempDir(), "relative-data")
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dataDir)
	t.Setenv("AO_DATA_DIR", "relative-data")
	capture := filepath.Join(t.TempDir(), "probe-dir")
	t.Setenv("AO_TEST_PROBE_DIR_FILE", capture)
	script := `#!/bin/sh
printf '%s' "$PWD" > "$AO_TEST_PROBE_DIR_FILE"
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"OK","usage":{"output_tokens":1}}'
`
	if err := os.WriteFile(p.binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx := ports.WithAgentAuthDataDir(t.Context(), dataDir)
	if status, err := p.AuthStatus(ctx); err != nil || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("canonical data-dir probe = %s, %v", status, err)
	}
	dir, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(string(dir)) != filepath.Join(dataDir, "auth-probes") {
		t.Fatalf("probe resolved relative override a second time: %q", dir)
	}
}
