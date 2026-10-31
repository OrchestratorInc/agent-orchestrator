// Agents a Chat session can switch to: the ones with a structured Chat driver.
export const CHAT_SWITCH_TARGETS = [
	"claude-code", "codex", "cursor", "opencode", "opencode-v2", "droid", "kimi",
	"kimchi", "pi", "omp", "qwen", "deepseek-harness", "unreal-agent", "coder-agents",
] as const;
// Agents with no terminal interface at all: they always open in Chat.
export const CHAT_ONLY_AGENTS: ReadonlySet<string> = new Set(["unreal-agent", "coder-agents"]);
// Agents a Terminal session can switch to: the Chat agents that also run in a terminal (Unreal
// Agent and Coder Agents are Chat-only), plus terminal agents that take the handoff note on their command line.
// Agents that have it typed in after start stay out until each passes a real switch.
// Gemini CLI is no longer supported.
export const TERMINAL_SWITCH_TARGETS = [
	...CHAT_SWITCH_TARGETS.filter((agent) => !CHAT_ONLY_AGENTS.has(agent)),
	"continue", "devin", "muse", "kilocode", "mimo-code", "prime-agent",
	"auggie", "copilot", "agy", "autohand", "vibe",
] as const;
// Terminal targets that answer one task and exit: greyed out with the reason there, still switchable in Chat.
export const TERMINAL_ONE_SHOT_TARGETS: ReadonlySet<string> = new Set(["deepseek-harness"]);
