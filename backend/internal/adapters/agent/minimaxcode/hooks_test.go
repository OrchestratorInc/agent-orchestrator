package minimaxcode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func hookLaunchArgument(profile, generation string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(filepath.Join(profile, "launches", generation)))
}

func TestRuntimeLaunchPinsHookCommandWithoutChangingTempPaths(t *testing.T) {
	t.Setenv("MINIMAX_DATA_DIR", t.TempDir())
	cfg := ports.WorkspaceHookConfig{
		DataDir:   filepath.Join(t.TempDir(), "AO data & (private)"),
		SessionID: "acme.dev-2", WorkspacePath: t.TempDir(),
	}
	plugin := New()
	if err := plugin.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Env = map[string]string{
		"AO_SESSION_ID": cfg.SessionID, "AO_DATA_DIR": cfg.DataDir,
		"AO_RUNTIME_LAUNCH_ID": "current-generation",
		"TMPDIR":               "original-unix-temp", "TEMP": "original-temp", "TMP": "original-tmp",
	}
	plugin.AugmentRuntimeLaunchEnv(cfg.Env, cfg.DataDir, domain.SessionID(cfg.SessionID), "current-generation")
	for key, want := range map[string]string{"TMPDIR": "original-unix-temp", "TEMP": "original-temp", "TMP": "original-tmp"} {
		if cfg.Env[key] != want {
			t.Fatalf("launch replaced %s with %q", key, cfg.Env[key])
		}
	}
	if err := plugin.PrepareRuntimeLaunch(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	profile, _ := profilePath(cfg.DataDir, cfg.SessionID)
	body, err := os.ReadFile(filepath.Join(profile, "plugins", "ao-activity", "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(body, &config); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure", "PermissionRequest", "Stop"} {
		groups := config.Hooks[event]
		if len(groups) != 1 || len(groups[0].Hooks) != 1 {
			t.Fatalf("missing managed hook for %s", event)
		}
		argument, ok := strings.CutPrefix(groups[0].Hooks[0].Command, `node "${CLAUDE_PLUGIN_ROOT}/hook.cjs" `)
		launch, err := base64.RawURLEncoding.DecodeString(argument)
		if !ok || err != nil || string(launch) != filepath.Join(profile, "launches", "current-generation") {
			t.Fatalf("%s command lost its immutable generation: %q, %v", event, launch, err)
		}
	}
}

func TestHookPinsIdentityAndRejectsReplacement(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for the native MiniMax hook contract")
	}
	source := t.TempDir()
	t.Setenv("MINIMAX_DATA_DIR", source)
	cfg := ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "acme.dev-2", WorkspacePath: t.TempDir()}
	p := New()
	if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Env = map[string]string{"AO_SESSION_ID": cfg.SessionID, "AO_DATA_DIR": cfg.DataDir, "AO_RUNTIME_LAUNCH_ID": "test-generation", "UNRELATED_SECRET": "must-not-copy"}
	if err := p.PrepareRuntimeLaunch(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	profile, _ := profilePath(cfg.DataDir, cfg.SessionID)
	identity := filepath.Join(profile, "ao-native-id")
	if err := os.WriteFile(identity, []byte("\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// MiniMax executes immutable cached copies, not the installed plugin path.
	hook := filepath.Join(profile, "v2", "plugin-hook-cache", "sha256-tree-v1-fixture", "hook.cjs")
	if err := os.MkdirAll(filepath.Dir(hook), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, hookSource, 0600); err != nil {
		t.Fatal(err)
	}
	first := "mvs_01234567890123456789012345678901"
	run := func(id string) []byte {
		t.Helper()
		cmd := exec.Command(node, hook, hookLaunchArgument(profile, "test-generation"))
		cmd.Env = append(os.Environ(), "PATH="+t.TempDir(), "TMPDIR="+t.TempDir())
		cmd.Stdin = strings.NewReader(`{"hook_event_name":"SessionStart","session_id":"` + id + `"}`)
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := run(first); len(got) != 0 {
		t.Fatalf("first session refused: %s", got)
	}
	got, _ := os.ReadFile(identity)
	if strings.TrimSpace(string(got)) != first {
		t.Fatal("native identity not pinned")
	}
	missingLaunch := exec.Command(node, hook)
	missingLaunch.Stdin = strings.NewReader(`{"hook_event_name":"SessionStart","session_id":"` + first + `"}`)
	if out, err := missingLaunch.Output(); err != nil || !strings.Contains(string(out), `"continue":false`) {
		t.Fatalf("hook accepted a missing launch argument: %s, %v", out, err)
	}
	preload := filepath.Join(t.TempDir(), "capture.cjs")
	capture := filepath.Join(t.TempDir(), "payload.json")
	preloadSource := `require('node:child_process').spawnSync=(_command,_args,options)=>{require('node:fs').writeFileSync(process.env.AO_HOOK_CAPTURE,options.input);return {status:0}};`
	if err := os.WriteFile(preload, []byte(preloadSource), 0600); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"PreToolUse", "PermissionRequest", "PostToolUse", "PostToolUseFailure"} {
		childPayload := fmt.Sprintf(`{"hook_event_name":%q,"session_id":"child-session","agent_id":"child-1","agent_type":"Explore"}`, event)
		cmd := exec.Command(node, "--require", preload, hook, hookLaunchArgument(profile, "test-generation"))
		cmd.Env = append(os.Environ(), "PATH="+t.TempDir(), "AO_HOOK_CAPTURE="+capture)
		cmd.Stdin = strings.NewReader(childPayload)
		if out, err := cmd.CombinedOutput(); err != nil || len(out) != 0 {
			t.Fatalf("%s child hook refused: %s %v", event, out, err)
		}
		forwarded, err := os.ReadFile(capture)
		if err != nil {
			t.Fatal(err)
		}
		var forwardedPayload struct {
			SessionID string `json:"session_id"`
			AgentID   string `json:"agent_id"`
			AgentType string `json:"agent_type"`
		}
		if err := json.Unmarshal(forwarded, &forwardedPayload); err != nil {
			t.Fatal(err)
		}
		if forwardedPayload.SessionID != first || forwardedPayload.AgentID != "child-1" || forwardedPayload.AgentType != "Explore" {
			t.Fatalf("%s forwarded child payload = %+v", event, forwardedPayload)
		}
	}
	got, _ = os.ReadFile(identity)
	if strings.TrimSpace(string(got)) != first {
		t.Fatal("child tool hook replaced pinned identity")
	}
	out := run("mvs_00000000000000000000000000000000")
	var rejection struct {
		Continue *bool  `json:"continue"`
		Message  string `json:"systemMessage"`
	}
	if json.Unmarshal(out, &rejection) != nil || rejection.Continue == nil || *rejection.Continue || rejection.Message == "" {
		t.Fatalf("replacement not refused: %s", out)
	}
	cmd := exec.Command(node, hook, hookLaunchArgument(profile, "test-generation"))
	cmd.Env = append(os.Environ(), "PATH="+t.TempDir())
	cmd.Stdin = strings.NewReader(`{"hook_event_name":"PreToolUse","session_id":"child-session","agent_id":"child-1"}`)
	if out, err := cmd.Output(); err != nil || !strings.Contains(string(out), `"continue":false`) {
		t.Fatalf("incomplete child provenance accepted: %s %v", out, err)
	}
	if err := os.Remove(identity); err != nil {
		t.Fatal(err)
	}
	if out := run(first); !strings.Contains(string(out), `"continue":false`) {
		t.Fatalf("missing guard accepted: %s", out)
	}
}

func TestOldHookKeepsItsRuntimeGeneration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for the MiniMax hook fixture")
	}
	source := t.TempDir()
	t.Setenv("MINIMAX_DATA_DIR", source)
	cfg := ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "fence", WorkspacePath: t.TempDir()}
	p := New()
	if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	profile, _ := profilePath(cfg.DataDir, cfg.SessionID)
	cfg.Env = map[string]string{"AO_SESSION_ID": cfg.SessionID, "AO_DATA_DIR": cfg.DataDir, "AO_RUNTIME_LAUNCH_ID": "generation-one", "SECRET": "not-routing"}
	if err := p.PrepareRuntimeLaunch(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(filepath.Join(profile, "launches", "generation-one", "routing.json"))
	if strings.Contains(string(first), "SECRET") {
		t.Fatal("unrelated credential copied into hook routing")
	}
	cfg.Env["AO_RUNTIME_LAUNCH_ID"] = "generation-two"
	if err := p.PrepareRuntimeLaunch(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	id := "mvs_01234567890123456789012345678901"
	if err := os.WriteFile(filepath.Join(profile, "ao-native-id"), []byte(id), 0600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "generations")
	script := fmt.Sprintf("#!%s\nrequire('node:fs').appendFileSync(%q, process.env.AO_RUNTIME_LAUNCH_ID+'\\n');\n", node, log)
	if err := os.WriteFile(filepath.Join(bin, "ao"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for _, generation := range []string{"generation-one", "generation-two"} {
		cmd := exec.Command(node, filepath.Join(profile, "plugins", "ao-activity", "hook.cjs"), hookLaunchArgument(profile, generation))
		cmd.Env = append(os.Environ(), "PATH="+bin)
		cmd.Stdin = strings.NewReader(`{"hook_event_name":"Stop","session_id":"` + id + `"}`)
		if out, err := cmd.CombinedOutput(); err != nil || len(out) != 0 {
			t.Fatalf("hook failed: %s %v", out, err)
		}
	}
	data, err := os.ReadFile(log)
	if err != nil || string(data) != "generation-one\ngeneration-two\n" {
		t.Fatalf("hook generations=%q, %v", data, err)
	}
}

func TestStandingHookUsesImmutableGenerationContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX callback fixture")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for the MiniMax hook contract")
	}
	source := t.TempDir()
	t.Setenv("MINIMAX_DATA_DIR", source)
	userRules := []byte("Native user rules must remain intact.\n")
	if err := os.WriteFile(filepath.Join(source, "AGENTS.md"), userRules, 0600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "private.txt")
	cfg := ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "context", WorkspacePath: t.TempDir(), SystemPromptFile: file}
	plugin := New()
	for _, turn := range []struct{ generation, context string }{{"first", "OLD_PRIVATE_CONTEXT"}, {"restored", "NEW_PRIVATE_CONTEXT"}} {
		if err := os.WriteFile(file, []byte(turn.context), 0600); err != nil {
			t.Fatal(err)
		}
		if err := plugin.GetAgentHooks(context.Background(), cfg); err != nil {
			t.Fatal(err)
		}
		cfg.Env = map[string]string{"AO_SESSION_ID": cfg.SessionID, "AO_DATA_DIR": cfg.DataDir, "AO_RUNTIME_LAUNCH_ID": turn.generation}
		if err := plugin.PrepareRuntimeLaunch(context.Background(), cfg); err != nil {
			t.Fatal(err)
		}
	}
	profile, _ := profilePath(cfg.DataDir, cfg.SessionID)
	for _, location := range []string{source, profile} {
		got, err := os.ReadFile(filepath.Join(location, "AGENTS.md"))
		if err != nil || string(got) != string(userRules) {
			t.Fatalf("native user rules changed: %q %v", got, err)
		}
	}
	id := "mvs_01234567890123456789012345678901"
	if err := os.WriteFile(filepath.Join(profile, "ao-native-id"), []byte(id), 0600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	callback := fmt.Sprintf("#!%s\nprocess.exit(Number(process.env.FIXTURE_CALLBACK_STATUS || '0'));\n", node)
	if err := os.WriteFile(filepath.Join(bin, "ao"), []byte(callback), 0700); err != nil {
		t.Fatal(err)
	}
	preload := filepath.Join(t.TempDir(), "callback-failure.cjs")
	failureFixture := `const child=require('node:child_process');
const spawn=child.spawnSync;
child.spawnSync=(...args)=>{
  if(process.env.FIXTURE_CALLBACK_STATUS==='timeout') return {status:null,error:Object.assign(new Error('timeout'),{code:'ETIMEDOUT'})};
  if(process.env.FIXTURE_CALLBACK_STATUS==='throw') throw new Error('reporting failed');
  return spawn(...args);
};`
	if err := os.WriteFile(preload, []byte(failureFixture), 0600); err != nil {
		t.Fatal(err)
	}
	// Exercise the actual managed helper, not a mock native provider.
	run := func(generation, event, nativeID, status string) map[string]any {
		t.Helper()
		callbackPath := bin
		if status == "missing" {
			callbackPath = t.TempDir()
		}
		cmd := exec.Command(node, "--require", preload, filepath.Join(profile, "plugins", "ao-activity", "hook.cjs"), hookLaunchArgument(profile, generation))
		cmd.Env = append(os.Environ(), "PATH="+callbackPath, "FIXTURE_CALLBACK_STATUS="+status)
		cmd.Stdin = strings.NewReader(fmt.Sprintf(`{"hook_event_name":%q,"session_id":%q}`, event, nativeID))
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		var value map[string]any
		if len(out) > 0 && json.Unmarshal(out, &value) != nil {
			t.Fatalf("invalid native hook output: %s", out)
		}
		return value
	}
	for _, turn := range []struct{ generation, context string }{{"first", "OLD_PRIVATE_CONTEXT"}, {"restored", "NEW_PRIVATE_CONTEXT"}} {
		value := run(turn.generation, "UserPromptSubmit", id, "0")
		output, ok := value["hookSpecificOutput"].(map[string]any)
		if !ok || output["hookEventName"] != "UserPromptSubmit" || output["additionalContext"] != turn.context {
			t.Fatalf("wrong generation context: %+v", value)
		}
		if _, ok := value["continue"]; ok {
			t.Fatal("context injection must not override native admission decisions")
		}
		info, err := os.Stat(filepath.Join(profile, "launches", turn.generation, "standing.txt"))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("private snapshot permissions: %v %v", info, err)
		}
	}
	if value := run("restored", "SessionStart", id, "0"); len(value) != 0 {
		t.Fatalf("duplicate context at SessionStart: %+v", value)
	}
	for _, status := range []string{"1", "missing", "timeout", "throw"} {
		value := run("restored", "UserPromptSubmit", id, status)
		output, ok := value["hookSpecificOutput"].(map[string]any)
		if !ok || output["additionalContext"] != "NEW_PRIVATE_CONTEXT" || value["continue"] != nil {
			t.Fatalf("activity report failure %q blocked native input: %+v", status, value)
		}
	}
	for _, status := range []string{"0", "1", "missing"} {
		value := run("restored", "UserPromptSubmit", "mvs_00000000000000000000000000000000", status)
		if value["continue"] != false || value["hookSpecificOutput"] != nil {
			t.Fatalf("replacement native identity admitted: %+v", value)
		}
	}
	if err := os.Remove(filepath.Join(profile, "launches", "restored", "standing.txt")); err != nil {
		t.Fatal(err)
	}
	if value := run("restored", "UserPromptSubmit", id, "0"); value["continue"] != false || value["hookSpecificOutput"] != nil {
		t.Fatalf("missing private instructions admitted: %+v", value)
	}
}

func TestLegacyIdentityAppendProfileFailsWithoutChangingHistory(t *testing.T) {
	t.Setenv("MINIMAX_DATA_DIR", t.TempDir())
	cfg := ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "legacy", WorkspacePath: t.TempDir()}
	plugin := New()
	if err := plugin.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	profile, _ := profilePath(cfg.DataDir, cfg.SessionID)
	history := filepath.Join(profile, "retained-history")
	original := []byte("Original native history must remain unchanged.\n")
	if err := os.WriteFile(history, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(profile, "ao-standing-transport")); err != nil {
		t.Fatal(err)
	}
	if err := plugin.GetAgentHooks(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "legacy identity-append") {
		t.Fatalf("legacy profile silently adopted: %v", err)
	}
	got, err := os.ReadFile(history)
	if err != nil || string(got) != string(original) {
		t.Fatalf("legacy history changed: %q %v", got, err)
	}
}
