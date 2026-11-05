// Package shellagent notices coding agents started by hand in a user's shell
// terminal and follows whether each is working, idle or waiting on the user.
// Nothing is stored; state lives in memory while the agent owns the terminal.
package shellagent

import (
	"path"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// executableHarness maps the command a harness installs onto its harness id.
// Names that collide with ordinary tools (Command Code's `cmd`, FX's `fx`) are
// left out: a wrong logo is worse than none.
var executableHarness = map[string]domain.AgentHarness{
	"claude":       domain.HarnessClaudeCode,
	"claude-code":  domain.HarnessClaudeCode,
	"codex":        domain.HarnessCodex,
	"aider":        domain.HarnessAider,
	"opencode":     domain.HarnessOpenCode,
	"grok":         domain.HarnessGrok,
	"droid":        domain.HarnessDroid,
	"amp":          domain.HarnessAmp,
	"agy":          domain.HarnessAgy,
	"crush":        domain.HarnessCrush,
	"cursor-agent": domain.HarnessCursor,
	"qwen":         domain.HarnessQwen,
	"gemini":       domain.HarnessGemini,
	"copilot":      domain.HarnessCopilot,
	"goose":        domain.HarnessGoose,
	"auggie":       domain.HarnessAuggie,
	"cn":           domain.HarnessContinue,
	"devin":        domain.HarnessDevin,
	"cline":        domain.HarnessCline,
	"kimi":         domain.HarnessKimi,
	"muse":         domain.HarnessMuse,
	"kiro-cli":     domain.HarnessKiro,
	"kilocode":     domain.HarnessKilocode,
	"kilo":         domain.HarnessKilocode,
	"vibe":         domain.HarnessVibe,
	"mistral-vibe": domain.HarnessVibe,
	"pi":           domain.HarnessPi,
	"kimchi":       domain.HarnessKimchi,
	"prime-agent":  domain.HarnessPrimeAgent,
	"autohand":     domain.HarnessAutohand,
	"omp":          domain.HarnessOMP,
	"unreal-agent": domain.HarnessUnreal,
	"codewhale":    domain.HarnessCodewhale,
	"codew":        domain.HarnessCodewhale,
	"mimo":         domain.HarnessMiMoCode,
	"memcode":      domain.HarnessMemcode,
	"dsh":          domain.HarnessDeepSeek,
	"openhands":    domain.HarnessOpenHands,
	"command-code": domain.HarnessCommandCode,
	"cmdc":         domain.HarnessCommandCode,
	"hermes":       domain.HarnessHermes,
}

// packageDirHarness names harnesses whose launcher runs a generic script from
// the package directory, like Cursor's `node …/cursor-agent/versions/<v>/index.js`.
var packageDirHarness = map[string]domain.AgentHarness{
	"cursor-agent": domain.HarnessCursor,
	"claude-code":  domain.HarnessClaudeCode,
	"gemini-cli":   domain.HarnessGemini,
	"opencode-ai":  domain.HarnessOpenCode,
	"qwen-code":    domain.HarnessQwen,
}

var interpreters = map[string]bool{"node": true, "nodejs": true, "bun": true, "deno": true, "python": true, "python3": true}

// ClassifyArgv names the harness a foreground command line runs, or "". Only
// the program counts (`vim codex.md` is vim), or an interpreter's script.
func ClassifyArgv(argv []string) domain.AgentHarness {
	if len(argv) == 0 {
		return ""
	}
	if harness := classifyProgram(argv[0]); harness != "" {
		return harness
	}
	interpreter := strings.TrimRight(baseName(argv[0]), "0123456789.")
	if !interpreters[interpreter] {
		return ""
	}
	for i := 1; i < min(len(argv), 5); i++ {
		arg := argv[i]
		if i == 1 && arg == "run" && (interpreter == "bun" || interpreter == "deno") {
			continue
		}
		flag, _, hasValue := strings.Cut(arg, "=")
		switch flag {
		case "-", "-e", "--eval", "-p", "--print", "--check":
			return ""
		case "--":
			if i+1 < len(argv) {
				return classifyProgram(argv[i+1])
			}
			return ""
		case "-c":
			if interpreter != "bun" && interpreter != "deno" {
				return ""
			}
			fallthrough
		case "-r", "--require", "--import", "--loader", "--experimental-loader", "--title", "--config", "-W", "-X":
			if !hasValue {
				i++
			}
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if strings.HasPrefix(arg, "-e") || strings.HasPrefix(arg, "-p") ||
				(strings.HasPrefix(arg, "-c") && interpreter != "bun" && interpreter != "deno") {
				return ""
			}
			if !hasValue {
				switch arg {
				case "--no-warnings", "--use-system-ca", "--smol", "--bun", "--allow-all", "-A", "-u", "-B", "-I", "-s", "-S", "-m":
				default:
					// An unknown option may consume the next argument as its value.
					return ""
				}
			}
			continue
		}
		return classifyProgram(arg)
	}
	return ""
}

func classifyProgram(arg string) domain.AgentHarness {
	name := baseName(arg)
	// Muse's launcher execs a versioned binary: muse-bin-1.4.14-R7309.1.
	if prefix, _, ok := strings.Cut(name, "-bin-"); ok {
		name = prefix
	}
	if harness, ok := executableHarness[name]; ok {
		return harness
	}
	for _, dir := range strings.Split(arg, "/") {
		if harness, ok := packageDirHarness[dir]; ok {
			return harness
		}
	}
	return ""
}

// baseName is a command's lowercased file name without a login shell's
// leading dash or a script extension.
func baseName(arg string) string {
	name := strings.TrimPrefix(strings.ToLower(path.Base(arg)), "-")
	for _, ext := range []string{".js", ".mjs", ".cjs", ".py"} {
		name = strings.TrimSuffix(name, ext)
	}
	return name
}
