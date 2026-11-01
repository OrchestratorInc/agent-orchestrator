import type { AgentId } from "./agent-options";

/**
 * Each agent's hue in oklch, taken from its brand. null is a black-and-white
 * brand mark, which reads as neutral. Lightness and chroma come from the theme
 * (--agent-tone-lightness / --agent-tone-chroma), so every agent holds the same
 * contrast in light and dark.
 */
const AGENT_HUES: Record<AgentId, number | null> = {
	"claude-code": 40,
	codex: 299,
	aider: 150,
	opencode: null,
	"opencode-v2": null,
	grok: null,
	droid: 70,
	amp: 15,
	agy: 260,
	crush: 345,
	cursor: null,
	qwen: 270,
	gemini: 245,
	copilot: null,
	goose: null,
	auggie: 160,
	continue: null,
	devin: 195,
	cline: null,
	kimi: null,
	muse: 320,
	kiro: 315,
	kilocode: 95,
	vibe: 55,
	pi: 180,
	kimchi: 25,
	"prime-agent": 225,
	autohand: 130,
	omp: 205,
	codewhale: 230,
	fx: 110,
	"unreal-agent": null,
	"mimo-code": 50,
	"deepseek-harness": 265,
	openhands: 85,
	"command-code": null,
	memcode: 140,
	hermes: 75,
};

/** The agent's theme colour as a CSS value; unknown harnesses read as neutral. */
export function agentTone(harness: string): string {
	const hue = AGENT_HUES[harness as AgentId] ?? null;
	return hue === null
		? "oklch(var(--agent-tone-lightness) 0 0)"
		: `oklch(var(--agent-tone-lightness) var(--agent-tone-chroma) ${hue})`;
}
