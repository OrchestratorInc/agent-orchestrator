/**
 * How AO's desktop and mobile clients name the models in Orchestrator Cloud's
 * model list, so one model reads the same on both. Each client builds its own
 * pickers from the names; this owns the naming so the two cannot drift apart.
 */

/** "Default", "Default (recommended)": a label that names no model. */
export function isDefaultPlaceholderLabel(label: string): boolean {
	return /^default(?:\s*\([^)]*\))?$/i.test(label.trim());
}

// The model a Claude list entry runs, as its description names it: "Haiku 4.5 · Fastest
// for quick answers", "Opus 5.5 with 1M context · Most capable".
export const CLAUDE_DESCRIBED_MODEL = /^\s*(?:claude\s+)?(fable|opus|sonnet|haiku)\s+(\d{1,2}(?:\.\d{1,2})?)(?![\d.])(\s+with\s+1m\s+context)?/i;

/**
 * A Cloud model's name, from the Cloud list alone. Claude names a family alias by its
 * family ("Haiku") and the model it runs in its description ("Haiku 4.5 · ..."). A
 * worker's CLI can run another model than this computer's for the same alias, so the
 * version comes from that description, never from this computer's catalog.
 */
export function cloudModelLabel(agentId: string, model: { displayName: string; description?: string }): string {
	const name = model.displayName;
	const described = agentId === "claude-code" ? CLAUDE_DESCRIBED_MODEL.exec(model.description ?? "") : null;
	// A name with a version of its own is kept, and so is a placeholder ("Default (recommended)").
	if (!described || !name || isDefaultPlaceholderLabel(name) || /\d/.test(name.replace(/\(.*?\)|\[.*?\]/g, ""))) return name;
	const family = new RegExp(`\\b${described[1]}\\b`, "i");
	return family.test(name) ? name.replace(family, (word) => `${word} ${described[2]}`) : name;
}
