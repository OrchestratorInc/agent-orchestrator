package memcode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestMemcodeSolWorkspacePreparationAndConfirmedEffort(t *testing.T) {
	ctx := context.Background()
	workspace, source, home := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("MEMCODE_ENDPOINT_URL", "")
	original := []byte(`{"pinned_model":"opus","pinned_window":1234,"models":{"coder":"sonnet"},"native_extra":true}`)
	for _, dir := range []string{workspace, source, home} {
		if err := os.MkdirAll(filepath.Join(dir, ".memcode"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, memcodeWorkspaceConfig), original, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p := New()
	cfg := ports.LaunchConfig{WorkspacePath: workspace, Config: ports.AgentConfig{Model: "sol"}}
	if _, err := p.AfterStartCommand(ctx, cfg); err == nil {
		t.Fatal("Sol startup accepted before workspace preparation")
	}
	if err := p.GetAgentHooks(ctx, ports.WorkspaceHookConfig{WorkspacePath: workspace, DataDir: t.TempDir(), SessionID: "ao-1", Config: cfg.Config}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(workspace, memcodeWorkspaceConfig))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Pinned string `json:"pinned_model"`
		Window int    `json:"pinned_window"`
		Models struct {
			Coder string `json:"coder"`
		} `json:"models"`
		Extra bool `json:"native_extra"`
	}
	if err := json.Unmarshal(data, &got); err != nil || got.Pinned != "sol" || got.Window != 1050000 || got.Models.Coder != "sonnet" || !got.Extra {
		t.Fatal("workspace pin changed unrelated native configuration", err)
	}
	for _, dir := range []string{source, home} {
		data, err := os.ReadFile(filepath.Join(dir, memcodeWorkspaceConfig))
		if err != nil || string(data) != string(original) {
			t.Fatal("source or global fixture was modified", err)
		}
	}
	command, err := p.AfterStartCommand(ctx, cfg)
	if err != nil || command != "/effort medium" {
		t.Fatal(command, err)
	}
	solTerminal := strings.Replace(readyTerminal, "glm-5.3-flash", "sol", 1)
	confirmation := "thinking effort → medium   (forced every turn this session · higher = more tokens)"
	for _, test := range []struct {
		output string
		want   bool
	}{
		{solTerminal, false},
		{confirmation + "\n" + solTerminal, true},
		{confirmation + "\n" + readyTerminal, false},
		{confirmation + "\n" + readyTerminal + "\nmemcode · v0.50.0 · sol · clean · opus · allow-all", false},
		{confirmation + "\n" + readyTerminal + "\nmemcode · v0.50.0 · sol · clean · opus · byok · ask", false},
		{confirmation + "\n" + readyTerminal + "\nmemcode · v0.50.0 · topic · clean · sol · allow-all", true},
		{confirmation + "\n" + readyTerminal + "\nmemcode · v0.50.0 · topic · clean · ↑10k ↓20 · ctx 1% · api.openai.com · sol · byok · ask", true},
		{confirmation + "\n" + solTerminal + "\nmemcode · v0.50.0 · sol · clean · opus · auto", false},
		{confirmation + "\n" + solTerminal + "\nmemcode · v0.50.0 · topic · clean · sol · 1 agent · ask", false},
		{confirmation + "\nthinking effort → off\n" + solTerminal, false},
		{confirmation + "\n" + strings.Replace(solTerminal, "Ask memcode…", "draft", 1), false},
	} {
		if p.AfterStartCommandApplied(cfg, test.output) != test.want {
			t.Fatalf("startup confirmation mismatch for %q", test.output)
		}
	}
}

func TestMemcodeSolInitializesRequiredNativeCoderTier(t *testing.T) {
	workspace := t.TempDir()
	if err := pinMemcodeSol(context.Background(), workspace); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(workspace, memcodeWorkspaceConfig))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if json.Unmarshal(data, &got) != nil || string(got["pinned_model"]) != `"sol"` || string(got["models"]) != `{"coder":"sol"}` {
		t.Fatal("new native config lacks a selected model or required coder tier")
	}
}

func TestMemcodeSolRefusesMalformedAndSymlinkedWorkspaceConfig(t *testing.T) {
	for _, body := range []string{"{", "null", `{"models":1}`, `{"models":{"coder":1}}`} {
		workspace := t.TempDir()
		if err := os.Mkdir(filepath.Join(workspace, ".memcode"), 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(workspace, memcodeWorkspaceConfig)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := pinMemcodeSol(context.Background(), workspace); err == nil {
			t.Fatal("malformed config overwritten")
		}
		if got, err := os.ReadFile(path); err != nil || string(got) != body {
			t.Fatal("refusal changed original bytes", err)
		}
	}
	workspace, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(workspace, ".memcode")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := pinMemcodeSol(context.Background(), workspace); err == nil {
		t.Fatal("symlinked provider directory accepted")
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatal("provider directory symlink was written through", err)
	}
}
