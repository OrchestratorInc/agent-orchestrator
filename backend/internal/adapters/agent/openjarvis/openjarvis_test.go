package openjarvis

import (
	"context"
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
		if err := p.ValidateTerminalMessage(message, "You> "); err == nil {
			t.Fatalf("accepted %q", message)
		}
	}
	for _, screen := range []string{"", "You> working", "You> Explain the marker You>\n", "You> draft ending You>", "You> old\nConfirm: Allow execution of tool 'shell_exec'? [y/N]", "Generation interrupted.", "Agent 'orchestrator' failed: missing tools"} {
		if err := p.ValidateTerminalMessage("yes", screen); err == nil {
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
	if err := New().GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "ao-1", Env: env, SystemPrompt: "AO standing"}); err != nil {
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
	if config["system_prompt"].(map[string]any)["prefix"] != "Native prefix\n\nAO standing" {
		t.Fatal("lost additive prefix")
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
	if ok || err == nil {
		t.Fatal("native restore fabricated")
	}
}
func TestUnsupportedPermissions(t *testing.T) {
	for _, mode := range []ports.PermissionMode{ports.PermissionModeAcceptEdits, ports.PermissionModeAuto, ports.PermissionModeBypassPermissions} {
		if _, err := New().GetLaunchCommand(context.Background(), ports.LaunchConfig{Permissions: mode}); err == nil {
			t.Fatalf("mode %s accepted", mode)
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
func TestMissingCodingToolsRefused(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[agent]\ndefault_agent='orchestrator'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if New().GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "test", Env: map[string]string{"OPENJARVIS_HOME": home}}) == nil {
		t.Fatal("plain chat config accepted")
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
