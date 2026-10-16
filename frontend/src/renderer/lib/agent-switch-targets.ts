// Agents a Chat session can switch to: the ones with a structured Chat driver.
export const CHAT_SWITCH_TARGETS = [
	"claude-code", "codex", "cursor", "opencode", "opencode-v2", "droid", "kimi",
	"kimchi", "pi", "omp", "qwen", "deepseek-harness", "unreal-agent",
] as const;
// Agents a Terminal session can switch to: the Chat agents that also run in a
// terminal (Unreal Agent is Chat-only), plus the terminal agents that receive
// the handoff note on their command line. Agents that have it typed in after
// start stay out until each has passed a real switch.
export const TERMINAL_SWITCH_TARGETS = [
	...CHAT_SWITCH_TARGETS.filter((agent) => agent !== "unreal-agent"),
	"continue", "gemini", "devin", "muse", "kilocode", "mimo-code", "prime-agent",
	"auggie", "copilot", "agy", "autohand", "vibe",
] as const;
