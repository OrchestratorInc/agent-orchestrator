package minimaxcode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestHookPinsIdentityAndRejectsReplacement(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for the native MiniMax hook contract")
	}
	source := t.TempDir()
	t.Setenv("MINIMAX_DATA_DIR", source)
	cfg := ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "test-1", WorkspacePath: t.TempDir()}
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
		cmd := exec.Command(node, hook)
		cmd.Env = append(os.Environ(), "PATH="+t.TempDir(), "TMPDIR="+filepath.Join(profile, "launches", "test-generation", "tmp"))
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
	preload := filepath.Join(t.TempDir(), "capture.cjs")
	capture := filepath.Join(t.TempDir(), "payload.json")
	preloadSource := `require('node:child_process').spawnSync=(_command,_args,options)=>{require('node:fs').writeFileSync(process.env.AO_HOOK_CAPTURE,options.input);return {status:0}};`
	if err := os.WriteFile(preload, []byte(preloadSource), 0600); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"PreToolUse", "PermissionRequest", "PostToolUse", "PostToolUseFailure"} {
		childPayload := fmt.Sprintf(`{"hook_event_name":%q,"session_id":"child-session","agent_id":"child-1","agent_type":"Explore"}`, event)
		cmd := exec.Command(node, "--require", preload, hook)
		cmd.Env = append(os.Environ(), "PATH="+t.TempDir(), "TMPDIR="+filepath.Join(profile, "launches", "test-generation", "tmp"), "AO_HOOK_CAPTURE="+capture)
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
	cmd := exec.Command(node, hook)
	cmd.Env = append(os.Environ(), "PATH="+t.TempDir(), "TMPDIR="+filepath.Join(profile, "launches", "test-generation", "tmp"))
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
		cmd := exec.Command(node, filepath.Join(profile, "plugins", "ao-activity", "hook.cjs"))
		cmd.Env = append(os.Environ(), "PATH="+bin, "TMPDIR="+filepath.Join(profile, "launches", generation, "tmp"))
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
