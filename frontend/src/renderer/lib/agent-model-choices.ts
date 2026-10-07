export function isConcreteModelID(id: string): boolean {
	return id !== "" && id.toLowerCase() !== "default";
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

/** The newest model of each Claude family, in Fable, Opus, Sonnet, Haiku order, then everything else, newest first. Models without a family and version stay current. */
export function splitClaudeModels<T extends { id: string; label: string }>(models: T[]): { current: T[]; other: T[] } {
	const ranked = models.flatMap((model) => {
		const parsed = claudeFamilyVersion(model);
		return parsed ? [{ model, ...parsed }] : [];
	});
	ranked.sort((a, b) => a.family - b.family || compareVersionsDesc(a.version, b.version));
	const current: T[] = [];
	const other: T[] = [];
	const seen = new Set<number>();
	for (const { model, family } of ranked) {
		(seen.has(family) ? other : current).push(model);
		seen.add(family);
	}
	current.push(...models.filter((model) => !claudeFamilyVersion(model)));
	return { current, other };
}

/** A configured family alias ("sonnet") duplicates that family's newest model, so mark that model as the default instead. */
export function foldClaudeAliasDefault<T extends { id: string; label: string; isDefault?: boolean }>(models: T[]): T[] {
	const alias = models.find((model) => model.isDefault && CLAUDE_FAMILIES.includes(model.id));
	if (!alias) return models;
	const rest = models.filter((model) => model !== alias);
	const family = CLAUDE_FAMILIES.indexOf(alias.id);
	const newest = splitClaudeModels(rest).current.find((model) => claudeFamilyVersion(model)?.family === family);
	return newest ? rest.map((model) => (model === newest ? { ...model, isDefault: true } : model)) : models;
}
