package shellagent

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestClassifyArgv(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want domain.AgentHarness
	}{
		{"native claude", []string{"claude"}, domain.HarnessClaudeCode},
		{"claude by path with prompt", []string{"/Users/me/.local/bin/claude", "fix the build"}, domain.HarnessClaudeCode},
		{"codex npm shim under node", []string{"node", "/opt/homebrew/bin/codex"}, domain.HarnessCodex},
		{"gemini under versioned node", []string{"/usr/local/bin/node22", "--no-warnings", "/usr/local/lib/node_modules/@google/gemini-cli/dist/index.js"}, domain.HarnessGemini},
		{"cursor agent launcher", []string{"/Users/me/.local/share/cursor-agent/versions/2025.1/node", "--use-system-ca", "/Users/me/.local/share/cursor-agent/versions/2025.1/index.js"}, domain.HarnessCursor},
		{"opencode binary", []string{"opencode"}, domain.HarnessOpenCode},
		{"muse versioned binary", []string{"/Users/me/.local/bin/muse-bin-1.4.14-R7309.1"}, domain.HarnessMuse},
		{"versioned binary of an unknown tool", []string{"/usr/local/bin/tool-bin-2.0"}, ""},
		{"aider under python", []string{"python3.12", "/Users/me/.local/bin/aider"}, domain.HarnessAider},
		{"login shell", []string{"-zsh"}, ""},
		{"plain shell", []string{"/bin/zsh", "-l"}, ""},
		{"editor opening a file named after an agent", []string{"vim", "codex.md"}, ""},
		{"node running an unrelated script", []string{"node", "server.js"}, ""},
		{"json viewer fx is not an agent", []string{"fx", "data.json"}, ""},
		{"empty", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyArgv(tt.argv); got != tt.want {
				t.Fatalf("ClassifyArgv(%q) = %q, want %q", tt.argv, got, tt.want)
			}
		})
	}
}

func TestLooksLikeApprovalPrompt(t *testing.T) {
	tests := []struct {
		name   string
		screen string
		want   bool
	}{
		{"claude permission dialog", "Bash command\n  rm -rf build\n\n Do you want to proceed?\n \x1b[36m❯ 1. Yes\x1b[0m\n   2. No", true},
		{"codex approval", "Would you like to run the following command?\n  $ npm test\n› 1. Yes, proceed", true},
		{"yes/no confirmation", "Overwrite config? (y/n)", true},
		{"idle prompt", "✻ Welcome back\n\n> \n  ? for shortcuts", false},
		{"question far above the live screen", "Do you want to proceed?\n" + strings.Repeat("output\n", 30) + "> ", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksLikeApprovalPrompt(tt.screen); got != tt.want {
				t.Fatalf("looksLikeApprovalPrompt() = %v, want %v", got, tt.want)
			}
		})
	}
}
