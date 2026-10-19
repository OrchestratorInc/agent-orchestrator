package cortexacp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestInstructionCandidateBoundsBeforeDriverCalls(t *testing.T) {
	for _, prompt := range []string{strings.Repeat("x", (64<<10)+1), string([]byte{0xff}), "context\x00suffix"} {
		cfg := isolatedConfig(t)
		cfg.SystemPrompt = prompt
		if _, err := NewInstructionCandidate(nil, nil).Start(context.Background(), cfg); !errors.Is(err, ports.ErrChatDriverIncompatible) {
			t.Fatalf("invalid context admitted: %v", err)
		}
	}
	if caps, err := NewInstructionCandidate(nil, nil).Probe(context.Background()); caps != nil || !errors.Is(err, ports.ErrChatDriverIncompatible) {
		t.Fatalf("experimental instructions opened production admission: %v %v", caps, err)
	}
}

func TestInstructionPluginEmitsOnlyPrivateAdditiveContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX hook command is explicitly unqualified on Windows")
	}
	cfg := isolatedConfig(t)
	// A quote in the path exercises the command boundary independently of data.
	cfg.DataDir = filepath.Join(cfg.DataDir, "owner's audit")
	if err := os.Mkdir(cfg.DataDir, 0700); err != nil {
		t.Fatal(err)
	}
	cfg.Env["HOME"] = filepath.Join(cfg.DataDir, "profile")
	if err := os.Mkdir(cfg.Env["HOME"], 0700); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(cfg.WorkspacePath, "AGENTS.md")
	if err := os.WriteFile(project, []byte("independent project rules"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.SystemPrompt = "private context\n'$(touch never-run)' `echo literal`\nUnicode: λ"
	args, _, err := configureInstructions(context.Background(), acpdriver.LaunchConfig{DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath, Env: cfg.Env, SystemPrompt: cfg.SystemPrompt}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 6 || args[0] != "acp" || args[1] != "serve" || args[4] != "--plugin-dir" {
		t.Fatalf("unexpected launch: %v", args)
	}
	pluginDir := args[5]
	info, err := os.Stat(pluginDir)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("plugin directory permissions: %v %v", info, err)
	}
	data, err := os.ReadFile(filepath.Join(pluginDir, ".cortex-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Name  string `json:"name"`
		Hooks map[string][]struct {
			Hooks []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	hooks := manifest.Hooks["UserPromptSubmit"]
	if len(manifest.Hooks) != 1 || len(hooks) != 1 || len(hooks[0].Hooks) != 1 || hooks[0].Hooks[0].Type != "command" {
		t.Fatalf("unexpected plugin: %s", data)
	}
	command := hooks[0].Hooks[0].Command
	if strings.Contains(command, cfg.SystemPrompt) {
		t.Fatal("context interpolated into shell command")
	}
	output, err := exec.CommandContext(context.Background(), "sh", "-c", command).Output()
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		HookSpecificOutput struct {
			Event   string `json:"hookEventName"`
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(output, &result) != nil || json.Unmarshal(output, &fields) != nil || len(fields) != 1 || result.HookSpecificOutput.Event != "UserPromptSubmit" || result.HookSpecificOutput.Context != cfg.SystemPrompt {
		t.Fatalf("hook result: %s", output)
	}
	for _, file := range []string{"context.json", filepath.Join(".cortex-plugin", "plugin.json")} {
		info, err := os.Stat(filepath.Join(pluginDir, file))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("private file permissions: %v %v", info, err)
		}
	}
	rules, err := os.ReadFile(project)
	if err != nil || string(rules) != "independent project rules" {
		t.Fatal("project rules modified")
	}
	// A second cold launch must use NEW context without rewriting a live plugin.
	next, _, err := configureInstructions(context.Background(), acpdriver.LaunchConfig{DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath, Env: cfg.Env, SystemPrompt: "new restore context"}, true)
	if err != nil || next[5] == pluginDir {
		t.Fatalf("restore reused live context: %v %v", next, err)
	}
	again, err := exec.CommandContext(context.Background(), "sh", "-c", command).Output()
	if err != nil || string(again) != string(output) {
		t.Fatal("cold launch mutated prior host context")
	}
}

func TestInstructionPluginRequiresExplicitOptIn(t *testing.T) {
	cfg := isolatedConfig(t)
	launch := acpdriver.LaunchConfig{DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath, Env: cfg.Env, SystemPrompt: "private context"}
	if _, _, err := configureInstructions(context.Background(), launch, false); !errors.Is(err, ports.ErrChatDriverIncompatible) {
		t.Fatalf("default gate opened: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := configureInstructions(ctx, launch, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled launch wrote context: %v", err)
	}
	if runtime.GOOS == "windows" {
		if _, _, err := configureInstructions(context.Background(), launch, true); !errors.Is(err, ports.ErrChatDriverIncompatible) {
			t.Fatalf("unsupported hook platform admitted: %v", err)
		}
	}
}
