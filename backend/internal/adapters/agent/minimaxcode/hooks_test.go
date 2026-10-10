package minimaxcode

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
	out := run("mvs_00000000000000000000000000000000")
	var rejection struct {
		Continue *bool  `json:"continue"`
		Message  string `json:"systemMessage"`
	}
	if json.Unmarshal(out, &rejection) != nil || rejection.Continue == nil || *rejection.Continue || rejection.Message == "" {
		t.Fatalf("replacement not refused: %s", out)
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
