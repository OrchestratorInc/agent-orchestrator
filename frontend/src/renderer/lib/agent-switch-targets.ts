// The agents a Chat session switches to before its host reports its own: the ones with a structured Chat driver.
export const CHAT_SWITCH_TARGETS = [
	"claude-code", "codex", "cursor", "opencode", "opencode-v2", "droid", "kimi",
	"kimchi", "pi", "omp", "qwen", "deepseek-harness", "unreal-agent", "agy",
] as const;
// Agents a Terminal session can switch to: the Chat agents that also run in a terminal (Unreal
// Agent is Chat-only), plus terminal agents that take the handoff note on their command line.
// Agents that have it typed in after start stay out until each passes a real switch.
// Gemini CLI is no longer supported.
export const TERMINAL_SWITCH_TARGETS = [
	...CHAT_SWITCH_TARGETS.filter((agent) => agent !== "unreal-agent"),
	"continue", "devin", "muse", "kilocode", "mimo-code", "prime-agent",
	"auggie", "copilot", "autohand", "vibe",
] as const;
// Terminal targets that answer one task and exit: greyed out with the reason there, still switchable in Chat.
export const TERMINAL_ONE_SHOT_TARGETS: ReadonlySet<string> = new Set(["deepseek-harness"]);
// The agents a Chat session on a host can switch to: every agent its daemon reports a Chat
// driver for, the ones above first in their order, then the rest in the daemon's. Until the
// daemon's list arrives, the ones above.
export function chatSwitchTargets(chatHarnesses?: readonly string[]): readonly string[] {
	if (!chatHarnesses?.length) return CHAT_SWITCH_TARGETS;
	const offered = new Set(chatHarnesses);
	const curated: ReadonlySet<string> = new Set(CHAT_SWITCH_TARGETS);
	return [...CHAT_SWITCH_TARGETS.filter((id) => offered.has(id)), ...chatHarnesses.filter((id) => !curated.has(id))];
}
