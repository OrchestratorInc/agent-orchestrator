package mastracode

import (
	"context"
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

func nativeBinary(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	name := "mastracode"
	if runtime.GOOS == "windows" {
		name += ".cmd"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestFreshRestartSelectsNewThreadWithoutDeletingHistory(t *testing.T) {
	p, cfg := prepared(t)
	db := history(t, cfg)
	nativeBinary(t)
	launch := ports.LaunchConfig{DataDir: cfg.DataDir, SessionID: cfg.SessionID, WorkspacePath: cfg.WorkspacePath, Prompt: "fresh task"}
	cmd, err := p.GetLaunchCommand(context.Background(), launch)
	if err != nil {
		t.Fatal(err)
	}
	if len(cmd) != 3 || cmd[1] != "--tui-initial-prompt" || cmd[2] != "fresh task" {
		t.Fatalf("fresh launch = %q", cmd)
	}
	delivery, err := p.GetPromptDeliveryStrategy(context.Background(), launch)
	if err != nil || delivery != ports.PromptDeliveryInCommand {
		t.Fatalf("task must be sent to new native history: %s,%v", delivery, err)
	}
	state, err := readState(cfg.WorkspacePath)
	if err != nil {
		t.Fatal(err)
	}
	if state.Database == filepath.Join(state.Profile, "mastra.db") {
		t.Fatal("fresh restart reused old native history")
	}
	if err := os.Remove(configPath(cfg.WorkspacePath, "ao-runtime.json")); err != nil {
		t.Fatal(err)
	}
	if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	restored, err := readState(cfg.WorkspacePath)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Database != state.Database {
		t.Fatal("recreated worktree lost exact native history path")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM mastra_messages WHERE thread_id=?`, testThread).Scan(&count); err != nil || count != 1 {
		t.Fatalf("history lost: %d, %v", count, err)
	}
}

func TestRestoreRetainsNativeModelSwitch(t *testing.T) {
	p, cfg := prepared(t)
	db := history(t, cfg)
	nativeBinary(t)
	if _, err := db.Exec(`UPDATE mastra_threads SET metadata=jsonb_set(metadata,'$.currentModelId','anthropic/changed-model')`); err != nil {
		t.Fatal(err)
	}
	_, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{DataDir: cfg.DataDir, Config: ports.AgentConfig{Model: "deepseek/deepseek-chat"}, Session: ports.SessionRef{ID: cfg.SessionID, WorkspacePath: cfg.WorkspacePath, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: testThread}}})
	if err != nil || !ok {
		t.Fatalf("restore after native model switch = %v, %v", ok, err)
	}
	cfg.Env["AO_RUNTIME_LAUNCH_ID"] = "g2"
	cfg.Env["MASTRACODE_MODEL_ID"] = "inherited/model"
	cfg.Config.Model = "deepseek/deepseek-chat"
	if err := p.PrepareRuntimeLaunch(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Env["MASTRACODE_MODEL_ID"] != "" {
		t.Fatal("restore overrides saved native model")
	}
}

func TestTranscriptDecisionMarkersDoNotBlockEmptyComposer(t *testing.T) {
	p := New()
	for _, marker := range []string{"Allow?", "Enter to submit", "Thread not found:", "Tool Approval Required"} {
		screen := "Agent quoted: " + marker + "\n" + emptyComposer
		if err := p.ValidateTerminalMessage(screen, "new task"); err != nil {
			t.Fatalf("transcript %q blocks delivery: %v", marker, err)
		}
		if state, ok := p.DetectTerminalActivity(screen); !ok || state != domain.ActivityIdle {
			t.Fatalf("transcript %q = %s,%v", marker, state, ok)
		}
	}
	screen := "╭────────────╮\n│ Allow?     │\n╰────────────╯\n▐ Mastra Code ▌"
	if state, ok := p.DetectTerminalActivity(screen); !ok || state != domain.ActivityWaitingInput {
		t.Fatalf("native dialog = %s,%v", state, ok)
	}
}

func TestTrackedMastraConfigIsUnchangedOnRefusal(t *testing.T) {
	for _, file := range []string{"hooks.json", "plugins/plugins.json", ".gitignore", "plugins/ao-runtime/index.ts"} {
		t.Run(file, func(t *testing.T) {
			p, cfg := prepared(t)
			path := configPath(cfg.WorkspacePath, filepath.FromSlash(file))
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"init"}, {"add", "-f", ".mastracode/" + file}} {
				cmd := exec.Command("git", args...)
				cmd.Dir = cfg.WorkspacePath
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %s, %v", args, out, err)
				}
			}
			cfg.SystemPrompt = "CHANGED"
			if err := p.GetAgentHooks(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "tracked") {
				t.Fatalf("tracked config accepted: %v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != string(original) {
				t.Fatal("tracked file was modified")
			}
		})
	}
}

func TestHistoryLockUsesPlatformLiveness(t *testing.T) {
	_, cfg := prepared(t)
	history(t, cfg)
	state, err := readState(cfg.WorkspacePath)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(state.Profile, "locks")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, testThread+".lock")
	for _, tc := range []struct {
		pid     int
		blocked bool
	}{{os.Getpid(), true}, {2147483647, false}} {
		if err := os.WriteFile(path, []byte(fmt.Sprint(tc.pid)), 0600); err != nil {
			t.Fatal(err)
		}
		err := validateHistory(context.Background(), cfg.WorkspacePath, testThread, ports.PermissionModeDefault, state)
		if (err != nil) != tc.blocked {
			t.Fatalf("pid %d: %v", tc.pid, err)
		}
	}
}

func TestUnrelatedProviderEnvironmentDoesNotConfigureMastra(t *testing.T) {
	p, cfg := prepared(t)
	t.Setenv("AO_DATA_DIR", cfg.DataDir)
	t.Setenv("MASTRA_APP_DATA_DIR", "")
	for _, key := range []string{"DEEPSEEK_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GOOGLE_GENERATIVE_AI_API_KEY", "MASTRA_GATEWAY_API_KEY"} {
		t.Setenv(key, "unrelated-tool-key")
	}
	status, err := p.AuthStatus(context.Background())
	if err != nil || status != ports.AgentAuthStatusUnknown {
		t.Fatalf("unconfigured profile = %s,%v", status, err)
	}
	state, err := readState(cfg.WorkspacePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(state.Profile, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeObject(filepath.Join(state.Profile, "settings.json"), map[string]any{"customProviders": []any{map[string]string{"apiKey": "mastra-key"}}}); err != nil {
		t.Fatal(err)
	}
	status, err = p.AuthStatus(context.Background())
	if err != nil || status != ports.AgentAuthStatusConfigured {
		t.Fatalf("configured profile = %s,%v", status, err)
	}
}

func TestPluginRefreshesInstructionsAfterHistoryHydration(t *testing.T) {
	_, cfg := prepared(t)
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node required for generated plugin integration")
	}
	plugin, err := os.ReadFile(configPath(cfg.WorkspacePath, "plugins", managedID, "index.ts"))
	if err != nil {
		t.Fatal(err)
	}
	source := strings.Replace(string(plugin), "export default", "const plugin =", 1)
	script := `
import assert from "node:assert/strict";
const timers=[];
globalThis.setInterval = fn => {const timer={fn,unref(){}};timers.push(timer);return timer;};
globalThis.clearInterval = timer => {timer.stopped=true;};
let state={pluginInstructions:["OTHER-PLUGIN"]};
let subscriber;
const session={thread:{getId:()=>"12345678-1234-1234-1234-123456789abc"},state:{get:()=>state,set:async update=>{state={...state,...update};}},subscribe:fn=>{subscriber=fn;return ()=>{};}};
const instructions=plugin.instructions({cwd:process.cwd(),getActiveSession:()=>session});
await timers[0].fn();
// The native thread id is assigned before saved metadata is hydrated.
state={pluginInstructions:["OTHER-PLUGIN","OLD-INSTRUCTIONS\n\nThe actual AO workspace for this session is "+process.cwd()+"."]};
subscriber?.({type:"thread_changed",threadId:session.thread.getId()});
for(let i=0;i<10;i++) await new Promise(resolve=>setImmediate(resolve));
assert.deepEqual(state.pluginInstructions,["OTHER-PLUGIN",instructions]);
// A second hydrate must replace AO instructions without accumulating copies.
subscriber?.({type:"thread_changed",threadId:session.thread.getId()});
for(let i=0;i<10;i++) await new Promise(resolve=>setImmediate(resolve));
assert.deepEqual(state.pluginInstructions,["OTHER-PLUGIN",instructions]);
const receipt=JSON.parse(fs.readFileSync(process.env.AO_MASTRA_RECEIPT_PATH,"utf8"));
assert.equal(receipt.nativeId,session.thread.getId());
assert.equal(receipt.generation,"g1");
`
	path := filepath.Join(cfg.WorkspacePath, "plugin-test.mjs")
	if err := os.WriteFile(path, []byte(source+script), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", path)
	cmd.Dir = cfg.WorkspacePath
	cmd.Env = append(os.Environ(), "AO_MASTRA_RECEIPT_PATH="+filepath.Join(cfg.DataDir, "receipt.json"), "AO_RUNTIME_LAUNCH_ID=g1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated plugin hydration: %v\n%s", err, out)
	}
}

func TestUnframedApprovalCannotAdmitInput(t *testing.T) {
	p := New()
	screen := "⚠ Tool Approval Required\n\nTool: execute_command\nCategory: Execute\n\nArguments:\n command: true\n\nAllow? yes  no  always allow execute  Yolo"
	if err := p.ValidateTerminalMessage(screen, "task"); err == nil {
		t.Fatal("approval accepted input")
	}
	if state, ok := p.DetectTerminalActivity(screen); !ok || state != domain.ActivityWaitingInput {
		t.Fatalf("approval=%s,%v", state, ok)
	}
}

func TestFailedFreshLaunchRetainsPriorExactRestore(t *testing.T) {
	p, cfg := prepared(t)
	history(t, cfg)
	original, err := readState(cfg.WorkspacePath)
	if err != nil {
		t.Fatal(err)
	}
	nativeBinary(t)
	if _, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{DataDir: cfg.DataDir, SessionID: cfg.SessionID, WorkspacePath: cfg.WorkspacePath, Prompt: "fresh"}); err != nil {
		t.Fatal(err)
	}
	// No new runtime was launched; AO still owns the original native UUID.
	if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	cmd, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{DataDir: cfg.DataDir, Session: ports.SessionRef{ID: cfg.SessionID, WorkspacePath: cfg.WorkspacePath, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: testThread}}})
	if err != nil || !ok {
		t.Fatalf("restore after failed fresh = %q,%v,%v", cmd, ok, err)
	}
	restored, err := readState(cfg.WorkspacePath)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Database != original.Database {
		t.Fatal("restore selected a different native database")
	}
}
