export function isConcreteModelID(id: string): boolean {
	return id !== "" && id.toLowerCase() !== "default";
}

/**
 * Leaves out the models switched off in Settings → Harness → Models. An empty
 * or unset list means every model, and models a catalog adds later are offered.
 * Ids in `keep` (a model a project, draft, or session already uses) stay listed
 * so a saved choice never vanishes. `modelID` maps an entry to its catalog id
 * when the list names models another way (Claude's chat aliases).
 * `narrowed` is true only when some catalog model was hidden.
 */
export function filterDisabledModels<T extends { id: string }>(
	models: T[],
	disabled: readonly string[] | undefined,
	keep: ReadonlyArray<string | undefined> = [],
	modelID: (model: T) => string = (model) => model.id,
): { models: T[]; narrowed: boolean } {
	if (!disabled?.length) return { models, narrowed: false };
	const kept = new Set(keep.filter((id): id is string => !!id));
	const hidden = new Set(disabled);
	const filtered = models.filter((model) => kept.has(model.id) || !hidden.has(modelID(model)));
	// A picker needs a model, so a list that switches off every model offers the whole catalog.
	if (filtered.length === 0 || filtered.length === models.length) return { models, narrowed: false };
	return { models: filtered, narrowed: true };
}

export function isDefaultPlaceholderLabel(label: string): boolean {
	return /^default(?:\s*\([^)]*\))?$/i.test(label.trim());
}

export function modelChoiceLabel(choice: { id: string; label: string }): string {
	return isDefaultPlaceholderLabel(choice.label) ? choice.id : choice.label || choice.id;
}

export function agentModelDisplayLabel(agentId: string | undefined, label: string): string {
	return agentId === "claude-code" ? label.replace(/^Claude\s+/i, "") : label;
}

const CLAUDE_FAMILIES = ["fable", "opus", "sonnet", "haiku"];

function claudeFamilyVersion(model: { id: string; label: string }) {
	for (const text of [modelChoiceLabel(model), model.id]) {
		// "(1M context)" and "[1m]" are not versions.
		const lower = text.toLowerCase().replace(/\(.*?\)|\[.*?\]/g, "");
		const family = CLAUDE_FAMILIES.findIndex((name) => lower.includes(name));
		// Major and minor only: skips snapshot dates and suffixes like "v1:0".
		const version = lower.match(/\d+/g)?.filter((part) => part.length <= 2).slice(0, 2).map(Number);
		if (family >= 0 && version?.length) return { family, version };
	}
	return undefined;
}

function compareVersionsDesc(a: number[], b: number[]) {
	for (let i = 0; i < Math.max(a.length, b.length); i += 1) {
		if ((a[i] ?? 0) !== (b[i] ?? 0)) return (b[i] ?? 0) - (a[i] ?? 0);
	}
	return 0;
}

// A context variant ("opus[1m]", "Opus 5.5 (1M context)") is its own choice,
// not an older release of its family.
function claudeVariant(model: { id: string; label: string }): string {
	const variant = model.id.match(/\[(.*?)\]/)?.[1] ?? model.label.match(/\((.*?)\)/)?.[1] ?? "";
	return variant.toLowerCase().replace(/\s*context\s*$/, "").replace(/\s+/g, "");
}

/** The newest model of each Claude family (and context variant), in Fable, Opus, Sonnet, Haiku order, then everything else, newest first. Models without a family and version stay current. */
export function splitClaudeModels<T extends { id: string; label: string }>(models: T[]): { current: T[]; other: T[] } {
	const ranked = models.flatMap((model) => {
		const parsed = claudeFamilyVersion(model);
		return parsed ? [{ model, ...parsed }] : [];
	});
	ranked.sort((a, b) => a.family - b.family || compareVersionsDesc(a.version, b.version));
	const current: T[] = [];
	const other: T[] = [];
	const seen = new Set<string>();
	for (const { model, family } of ranked) {
		const slot = `${family}${claudeVariant(model)}`;
		(seen.has(slot) ? other : current).push(model);
		seen.add(slot);
	}
	current.push(...models.filter((model) => !claudeFamilyVersion(model)));
	return { current, other };
}

/**
 * Claude's chat model option offers family aliases ("opus", "sonnet[1m]") and bare ids.
 * Maps a choice to its catalog entry: an id to itself, an alias to the newest model of
 * its family and context variant.
 */
export function claudeChoiceModel<T extends { id: string; label: string }>(catalog: T[]): ((value: string) => T | undefined) | undefined {
	if (catalog.length === 0) return undefined;
	const newest = splitClaudeModels(catalog).current;
	return (value: string) => {
		const id = value.toLowerCase();
		const exact = catalog.find((model) => model.id.toLowerCase() === id);
		if (exact) return exact;
		const family = id.replace(/\[.*?\]$/, "");
		const variant = claudeVariant({ id, label: "" });
		return newest.find((model) => claudeVariant(model) === variant
			&& (model.id.toLowerCase().includes(`-${family}-`)
				|| agentModelDisplayLabel("claude-code", model.label).toLowerCase().startsWith(family)));
	};
}

/** A configured family alias ("sonnet") duplicates that family's newest model, so mark that model as the default instead. */
export function foldClaudeAliasDefault<T extends { id: string; label: string; isDefault?: boolean }>(models: T[]): T[] {
	const alias = models.find((model) => model.isDefault && CLAUDE_FAMILIES.includes(model.id));
	if (!alias) return models;
	const rest = models.filter((model) => model !== alias);
	const family = CLAUDE_FAMILIES.indexOf(alias.id);
	const newest = splitClaudeModels(rest).current.find((model) => claudeFamilyVersion(model)?.family === family && claudeVariant(model) === "");
	return newest ? rest.map((model) => (model === newest ? { ...model, isDefault: true } : model)) : models;
}
