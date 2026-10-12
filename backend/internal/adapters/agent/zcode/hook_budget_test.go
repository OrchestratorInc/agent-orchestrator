package zcode

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestProtocolPreservesCompetingHooksByRejectingUnboundedContext(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(map[bool]string{false: "workspace", true: "global"}[global], func(t *testing.T) {
			home, workspace := t.TempDir(), t.TempDir()
			path := filepath.Join(workspace, ".zcode", "config.json")
			if global {
				path = filepath.Join(home, ".zcode", "cli", "config.json")
			}
			input := []byte(`{"hooks":{"enabled":true,"events":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"user-owned-hook"}]}]}}}`)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, input, 0o600); err != nil {
				t.Fatal(err)
			}
			cfg := ports.WorkspaceHookConfig{WorkspacePath: workspace, Env: map[string]string{"HOME": home}}
			if err := ValidateProtocolHookBudget(context.Background(), cfg); err == nil {
				t.Fatal("unbounded combined context accepted")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(input) {
				t.Fatal("user native hook was changed instead of refused")
			}
		})
	}
}

func TestProtocolRejectsReducedNativeOutputBudgetWithoutChangingConfig(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "zcode.json")
	input := []byte(`{"hooks":{"enabled":true,"maxOutputBytes":1024}}`)
	if err := os.WriteFile(path, input, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := ports.WorkspaceHookConfig{WorkspacePath: workspace, Env: map[string]string{"HOME": t.TempDir()}}
	if err := ValidateProtocolHookBudget(context.Background(), cfg); err == nil {
		t.Fatal("native stdout truncation configuration accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(input) {
		t.Fatal("native configuration changed")
	}
}
