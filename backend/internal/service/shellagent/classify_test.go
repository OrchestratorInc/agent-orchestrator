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
		{"stdin script with an agent argument", []string{"node", "-", "codex"}, ""},
		{"unknown option value is not a script", []string{"node", "--env-file", "codex", "server.js"}, ""},
		{"option delimiter ends flag parsing", []string{"node", "--", "--other-script", "codex"}, ""},
		{"inline option before an agent script", []string{"node", "--max-old-space-size=2048", "codex.js"}, domain.HarnessCodex},
		{"unbuffered Python agent", []string{"python3", "-u", "aider"}, domain.HarnessAider},
		{"agent name is only a script argument", []string{"node", "server.js", "codex"}, ""},
		{"agent name is only a Python argument", []string{"python3", "report.py", "claude"}, ""},
		{"node eval is not an agent executable", []string{"node", "--eval", "codex"}, ""},
		{"inline eval followed by an agent name", []string{"node", "--eval=console.log(1)", "codex"}, ""},
		{"compact eval followed by an agent name", []string{"node", "-econsole.log(1)", "codex"}, ""},
		{"Python code is not an agent executable", []string{"python3", "-c", "claude"}, ""},
		{"compact Python code followed by an agent name", []string{"python3", "-cprint(1)", "claude"}, ""},
		{"required module is not the script", []string{"node", "--require", "codex", "server.js"}, ""},
		{"node preload before an agent script", []string{"node", "--require", "init.js", "/usr/local/bin/codex"}, domain.HarnessCodex},
		{"Python module launch", []string{"python3", "-m", "aider"}, domain.HarnessAider},
		{"bun config before an agent script", []string{"bun", "-c", "bunfig.toml", "codex.js"}, domain.HarnessCodex},
		{"deno config before an agent script", []string{"deno", "run", "--config", "deno.json", "codex.js"}, domain.HarnessCodex},
		{"bun run with a config file", []string{"bun", "run", "-c", "bunfig.toml", "codex.js"}, domain.HarnessCodex},
		{"deno run with ordinary script arguments", []string{"deno", "run", "server.js", "codex"}, ""},
		{"agent name is only a config value", []string{"bun", "-c", "codex", "server.js"}, ""},
		{"inline config before a plain script", []string{"bun", "--config=codex", "server.js"}, ""},
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
