export const CHAT_SWITCH_TARGETS = [
	"claude-code", "codex", "cursor", "opencode", "opencode-v2", "droid", "kimi",
	"kimchi", "pi", "omp", "qwen", "deepseek-harness", "unreal-agent",
] as const;
export const TERMINAL_SWITCH_TARGETS = [
	...CHAT_SWITCH_TARGETS.filter((agent) => agent !== "unreal-agent"),
	"continue", "devin", "muse", "kilocode", "mimo-code", "prime-agent",
	"auggie", "copilot", "agy", "autohand", "vibe",
] as const;
export const TERMINAL_ONE_SHOT_TARGETS: ReadonlySet<string> = new Set(["deepseek-harness"]);
