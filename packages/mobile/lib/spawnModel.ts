import type { AgentModelInfo } from "./api";

export type SpawnModelDefaults = {
	selectedAgent: string;
	projectWorkerAgent?: string;
	projectWorkerModel?: string;
};

export type SpawnModelSource = { projectId: string | null; agentId: string };

export type SpawnAgentDefaults = {
	projectWorkerAgent?: string;
	projectAgent?: string;
	availableAgents: readonly string[];
};

export function resolveSpawnAgent(input: SpawnAgentDefaults): string {
	for (const candidate of [input.projectWorkerAgent, input.projectAgent]) {
		if (candidate && input.availableAgents.includes(candidate)) return candidate;
	}
	return input.availableAgents[0] ?? "";
}

export function spawnModelSourceChanged(current: SpawnModelSource, next: SpawnModelSource): boolean {
	return current.projectId !== next.projectId || current.agentId !== next.agentId;
}

/**
 * The model the project pins for the agent about to be spawned, or "" for none.
 *
 * Only the project's own configuration counts. The catalog's `isDefault` entry is
 * deliberately **not** consulted: it is a claim about which model the provider
 * will choose, and it is not always true. Codex advertises `gpt-6-astra` as its
 * default while a fresh thread comes up on `gpt-5.6-sol`, so the spawn sheet
 * named Astra and the chat named Sol. Nothing on this side can know what a
 * provider will really pick, so nothing here pretends to.
 */
export function resolveSpawnModel(input: SpawnModelDefaults): string {
	if (input.selectedAgent !== input.projectWorkerAgent) return "";
	return (input.projectWorkerModel ?? "").trim();
}

/**
 * What to send as the model.
 *
 * A picked value is sent even when the project pins the same one. The old rule
 * dropped it as redundant, so choosing the model the sheet had *shown* as the
 * default sent no model at all: the session recorded nothing, and the chat had no
 * way to know what was asked for, so it displayed whatever the thread happened to
 * be running. Sending the pick is what makes "you started this with Astra" true
 * in both places.
 *
 * `touched` is false for the untouched "Automatic" state, which sends nothing and
 * lets the provider choose — the one case where no model is the honest answer.
 */
export function modelOverride(value: string, touched: boolean): string | undefined {
	if (!touched) return undefined;
	const clean = value.trim();
	return clean && clean !== "__auto__" ? clean : undefined;
}

/** How many of a catalog's models an agent's menu lists before "All models". */
export const MENU_MODEL_LIMIT = 12;

/**
 * One agent's models as its menu offers them. A menu builds every row up front,
 * and some catalogs hold thousands (opencode's lists every provider's), so it
 * lists the first few and the whole catalog opens in a sheet. The picked model
 * is always among them, so the menu can show it checked.
 */
export function menuModels(models: readonly AgentModelInfo[], selected: string, limit = MENU_MODEL_LIMIT): { models: { id: string; label: string }[]; total: number; defaultLabel?: string } {
	const shown = models.slice(0, limit);
	const picked = selected && !shown.some((model) => model.id === selected) ? models.find((model) => model.id === selected) : undefined;
	return {
		models: [...shown, ...(picked ? [picked] : [])].map((model) => ({ id: model.id, label: model.label })),
		total: models.length,
		defaultLabel: models.find((model) => model.isDefault)?.label,
	};
}

/** An agent's default-model row, by the model's name when the catalog marks one. */
export function defaultModelChoice(defaultLabel?: string): string {
	return defaultLabel ? `Default · ${defaultLabel}` : "Default model";
}

/**
 * The model whose effort levels the composer offers while the task runs the
 * default model and the catalog marks none (Claude Code's never does): the
 * model desktop's New task would open on, Claude Code's newest Opus, else the
 * first that takes effort. Picking a level pins this model, since the server
 * cannot check a level against a model it cannot name.
 */
export function defaultEffortModel(harness: string, models: readonly AgentModelInfo[]): AgentModelInfo | undefined {
	const tunable = models.filter((model) => model.efforts?.length);
	return (harness === "claude-code" ? tunable.find((model) => /opus/i.test(model.label || model.id)) : undefined) ?? tunable[0];
}

const CLAUDE_FAMILIES = ["fable", "opus", "sonnet", "haiku"];

/**
 * The model a Claude family alias ("opus") runs, for its effort levels: the
 * family's newest model that takes effort. The alias carries no levels of its
 * own; the CLI resolves it to that model, and the server checks a level
 * against it (session_manager's newestClaudeFamilyModel).
 */
export function claudeAliasModel(models: readonly AgentModelInfo[], alias: string): AgentModelInfo | undefined {
	const family = alias.trim().toLowerCase().replace(/\[1m\]$/, "");
	if (!CLAUDE_FAMILIES.includes(family)) return undefined;
	let best: AgentModelInfo | undefined;
	let bestVersion: number[] = [];
	for (const model of models) {
		if (!model.efforts?.length) continue;
		const version = claudeFamilyVersion(model, family);
		if (version && (!best || compareVersions(version, bestVersion) > 0)) [best, bestVersion] = [model, version];
	}
	return best;
}

// major.minor from a model's label or id, or undefined outside the family.
// Snapshot dates run past two digits and are ignored.
function claudeFamilyVersion(model: AgentModelInfo, family: string): number[] | undefined {
	for (const text of [model.label, model.id]) {
		const lower = text.toLowerCase().replace(/\(.*?\)|\[.*?\]/g, "");
		if (!lower.includes(family)) continue;
		const version = (lower.match(/\d+/g) ?? []).filter((part) => part.length <= 2).slice(0, 2).map(Number);
		if (version.length) return version;
	}
	return undefined;
}

function compareVersions(a: readonly number[], b: readonly number[]): number {
	for (let i = 0; i < Math.max(a.length, b.length); i++) {
		const diff = (a[i] ?? 0) - (b[i] ?? 0);
		if (diff) return diff;
	}
	return 0;
}
