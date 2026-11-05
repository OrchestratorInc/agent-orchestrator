// Package shellagent notices coding agents started by hand inside a user's
// shell terminal and follows whether each one is working, done, or waiting on
// the user. It never stores anything: state lives in memory for as long as the
// agent owns the terminal's foreground.
package shellagent

import (
	"path"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// executableHarness maps the command a harness installs onto AO's harness id.
// Names generic enough to collide with ordinary tools (Command Code's `cmd`,
// FX's `fx`, which is also a popular JSON viewer) are left out: a wrong agent
// logo on a shell tab is worse than none.
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

// packageDirHarness names harnesses by a directory their files live under,
// for launchers that run an interpreter on a script with a generic name
// (Cursor's `node …/cursor-agent/versions/<v>/index.js`).
var packageDirHarness = map[string]domain.AgentHarness{
	"cursor-agent": domain.HarnessCursor,
	"claude-code":  domain.HarnessClaudeCode,
	"gemini-cli":   domain.HarnessGemini,
	"opencode-ai":  domain.HarnessOpenCode,
	"qwen-code":    domain.HarnessQwen,
}

// interpreters run a script that names the real program in a later argument.
var interpreters = map[string]bool{
	"node": true, "nodejs": true, "bun": true, "deno": true, "tsx": true,
	"python": true, "python3": true, "uv": true, "uvx": true, "pipx": true,
}

// maxScriptArgs bounds how far past an interpreter the classifier looks.
const maxScriptArgs = 4

// ClassifyArgv names the harness a foreground command line is running, or ""
// for anything else (the user's shell, an editor, a build). Only the program
// itself counts: `vim codex.md` is vim. An interpreter's script is the program
// when it is the interpreter's first non-flag arguments.
func ClassifyArgv(argv []string) domain.AgentHarness {
	if len(argv) == 0 {
		return ""
	}
	if harness := classifyProgram(argv[0]); harness != "" {
		return harness
	}
	if !interpreters[trimVersion(baseName(argv[0]))] {
		return ""
	}
	seen := 0
	for _, arg := range argv[1:] {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if harness := classifyProgram(arg); harness != "" {
			return harness
		}
		if seen++; seen >= maxScriptArgs {
			break
		}
	}
	return ""
}

func classifyProgram(arg string) domain.AgentHarness {
	if harness, ok := executableHarness[baseName(arg)]; ok {
		return harness
	}
	for _, segment := range strings.Split(filepathSlash(arg), "/") {
		if harness, ok := packageDirHarness[strings.ToLower(segment)]; ok {
			return harness
		}
	}
	return ""
}

// baseName lowercases a command's file name without its directory, a login
// shell's leading dash, or a script/executable extension.
func baseName(arg string) string {
	name := strings.ToLower(path.Base(filepathSlash(arg)))
	name = strings.TrimPrefix(name, "-")
	for _, ext := range []string{".js", ".mjs", ".cjs", ".ts", ".py", ".exe", ".cmd"} {
		if strings.HasSuffix(name, ext) {
			return strings.TrimSuffix(name, ext)
		}
	}
	return name
}

// trimVersion drops a version suffix from an interpreter name (python3.12).
func trimVersion(name string) string {
	return strings.TrimRight(name, "0123456789.")
}

func filepathSlash(arg string) string { return strings.ReplaceAll(arg, `\`, "/") }
