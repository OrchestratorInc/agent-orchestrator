package strands

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestHooksStayOutsideWorkspace(t *testing.T) {
	p, cfg := setup(t, `{}`)
	cmd, err := p.GetLaunchCommand(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(cmd, " ")
	if !strings.Contains(joined, hookPath(cfg.DataDir)) {
		t.Fatal(cmd)
	}
	before, err := os.ReadFile(hookPath(cfg.DataDir))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(hookPath(cfg.DataDir))
	if string(before) != string(after) {
		t.Fatal("not idempotent")
	}
	entries, _ := os.ReadDir(cfg.WorkspacePath)
	if len(entries) != 0 {
		t.Fatal("workspace mutated")
	}
	if filepath.Dir(hookPath(cfg.DataDir)) == cfg.WorkspacePath {
		t.Fatal("hook in workspace")
	}
}

func TestActivityUsesInvocationAndPermissionCallbacks(t *testing.T) {
	for event, want := range map[string]domain.ActivityState{"session-start": domain.ActivityIdle, "active": domain.ActivityActive, "permission-request": domain.ActivityBlocked, "stop": domain.ActivityIdle} {
		got, ok := DeriveActivityState(event, nil)
		if !ok || got != want {
			t.Fatalf("%s: %s %v", event, got, ok)
		}
	}
	if _, ok := DeriveActivityState("before-tool", nil); ok {
		t.Fatal("tool is not an invocation")
	}
}

func TestComposerRequiresEmptyEditorAndFooter(t *testing.T) {
	p := New()
	ready := "▌Enter to send • Ctrl+J for newline • / for commands\n\nmodel • Auto /workspace /settings"
	if !p.ComposerIsEmpty(ready) {
		t.Fatal("ready editor not recognized")
	}
	for _, input := range []string{"", "Loading…", strings.ReplaceAll(ready, "▌Enter to send • Ctrl+J for newline • / for commands", "draft text"), ready + "\nPermission required", "▌Enter to send • Ctrl+J for newline • / for commands"} {
		if p.ComposerIsEmpty(input) {
			t.Fatalf("not ready: %q", input)
		}
	}
}

func TestAuthDoesNotClaimAuthorization(t *testing.T) {
	p, _ := setup(t, `{"profile":{"model":"litellm/fixture"}}`)
	for _, key := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "LITELLM_API_KEY", "AWS_BEARER_TOKEN_BEDROCK", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"} {
		t.Setenv(key, "")
	}
	status, err := p.AuthStatus(context.Background())
	if err != nil || status != ports.AgentAuthStatusUnknown {
		t.Fatalf("%s %v", status, err)
	}
	t.Setenv("LITELLM_API_KEY", "fixture-not-a-real-secret")
	status, err = p.AuthStatus(context.Background())
	if err != nil || status != ports.AgentAuthStatusConfigured {
		t.Fatalf("%s %v", status, err)
	}
}

// Project Node processes inherit NODE_OPTIONS from Strands' workspace sandbox.
// Only the Strands process may hide the synchronous module-hook API.
func TestNodePreloadLeavesProjectChildrenNative(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	baseline := exec.Command(node, "--input-type=module", "-e", `import * as m from "node:module"; process.exit(typeof m.registerHooks === "function" ? 0 : 77)`)
	baseline.Env = append(os.Environ(), "NODE_OPTIONS=", "AO_STRANDS_NODE_HOOK_WORKAROUND=")
	if err := baseline.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 77 {
			t.Skip("Node lacks synchronous module hooks")
		}
		t.Fatal(err)
	}
	p, cfg := setup(t, `{}`)
	if err := p.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{DataDir: cfg.DataDir}); err != nil {
		t.Fatal(err)
	}
	for _, scoped := range []bool{false, true} {
		name := "unscoped"
		if scoped {
			name = "strands-parent"
		}
		t.Run(name, func(t *testing.T) {
			env := map[string]string{"NODE_OPTIONS": "--conditions=ao-strands-preload-test", "AO_STRANDS_CHILD_ENV": "preserved"}
			p.AugmentRuntimeLaunchEnv(env, cfg.DataDir, "ao-1", "launch-1")
			if !scoped {
				delete(env, "AO_STRANDS_NODE_HOOK_WORKAROUND")
			}
			script := `import assert from "node:assert/strict";
import {spawnSync} from "node:child_process";
import * as m from "node:module";
const scoped = process.argv[1] === "true";
assert.equal(typeof m.registerHooks, scoped ? "undefined" : "function", "parent module-hook API");
const child = spawnSync(process.execPath, ["--input-type=module", "-e", ` + "`" + `
import assert from "node:assert/strict";
import {createRequire, registerHooks} from "node:module";
assert.equal(typeof registerHooks, "function", "project child must retain native ESM hooks");
assert.equal(typeof createRequire(import.meta.url)("node:module").registerHooks, "function", "project child must retain native CJS hooks");
registerHooks({}).deregister();
assert.equal(process.env.AO_STRANDS_NODE_HOOK_WORKAROUND, undefined);
assert.equal(process.env.AO_STRANDS_CHILD_ENV, "preserved");
assert.equal(process.env.NODE_OPTIONS, process.argv[1]);
console.log("native-child-ok");
` + "`" + `, "--", process.env.NODE_OPTIONS], {encoding:"utf8"});
assert.equal(child.status, 0, child.stderr);
assert.equal(child.stdout.trim(), "native-child-ok");
assert.equal(process.env.AO_STRANDS_NODE_HOOK_WORKAROUND, undefined, "one-use flag must be consumed");
console.log("parent-child-ok");`
			scopedArg := "false"
			if scoped {
				scopedArg = "true"
			}
			command := exec.Command(node, "--input-type=module", "-e", script, scopedArg)
			command.Env = append(os.Environ(), "AO_STRANDS_NODE_HOOK_WORKAROUND=")
			for key, value := range env {
				command.Env = append(command.Env, key+"="+value)
			}
			out, err := command.CombinedOutput()
			if err != nil || strings.TrimSpace(string(out)) != "parent-child-ok" {
				t.Fatalf("parent/child output %q, %v", out, err)
			}
		})
	}
}

func TestRuntimeLaunchEnvScopesNodeHookWorkaround(t *testing.T) {
	env := map[string]string{}
	New().AugmentRuntimeLaunchEnv(env, t.TempDir(), "ao-1", "launch-1")
	if env["AO_STRANDS_NODE_HOOK_WORKAROUND"] != "1" {
		t.Fatal("Strands launch must enable its one-use module-hook workaround")
	}
}
