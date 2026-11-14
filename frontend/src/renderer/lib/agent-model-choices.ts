import { collapseModelVariants, listsModelVariants } from "./model-variants";

export function isConcreteModelID(id: string): boolean {
	return id !== "" && id.toLowerCase() !== "default";
}

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
	if (filtered.length === 0 || filtered.length === models.length) return { models, narrowed: false };
	return { models: filtered, narrowed: true };
}

export function offeredCatalogModels<T extends { id: string; provider?: string }>(
	catalog: { models: T[]; configuredProviders?: string[]; runnableModels?: string[] } | undefined,
	disabled: readonly string[] | undefined,
	keep: ReadonlyArray<string | undefined> = [],
): T[] {
	const models = catalog?.models ?? [];
	const runnable = new Set([...(catalog?.runnableModels ?? []), ...keep.filter((id): id is string => Boolean(id))]);
	const configured = new Set(catalog?.configuredProviders ?? []);
	const usable = catalog?.runnableModels?.length
		? models.filter((model) => runnable.has(model.id))
		: configured.size > 0 ? models.filter((model) => !model.provider || configured.has(model.provider)) : models;
	return filterDisabledModels(usable.length > 0 ? usable : models, disabled, keep).models;
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

const CLAUDE_DESCRIBED_MODEL = /^\s*(?:claude\s+)?(fable|opus|sonnet|haiku)\s+(\d{1,2}(?:\.\d{1,2})?)(?![\d.])(\s+with\s+1m\s+context)?/i;

export function cloudModelLabel(agentId: string, model: { displayName: string; description?: string }): string {
	const name = model.displayName;
	const described = agentId === "claude-code" ? CLAUDE_DESCRIBED_MODEL.exec(model.description ?? "") : null;
	if (!described || !name || isDefaultPlaceholderLabel(name) || /\d/.test(name.replace(/\(.*?\)|\[.*?\]/g, ""))) return name;
	const family = new RegExp(`\\b${described[1]}\\b`, "i");
	return family.test(name) ? name.replace(family, (word) => `${word} ${described[2]}`) : name;
}

export function cloudDefaultModel<T extends { id: string; label: string }>(
	catalog: ReadonlyArray<{ id: string; description?: string; default: boolean }>,
	offered: T[],
): T | undefined {
	const marked = catalog.find((model) => model.default);
	if (!marked) return undefined;
	if (isConcreteModelID(marked.id)) return offered.find((model) => model.id === marked.id);
	const described = CLAUDE_DESCRIBED_MODEL.exec(marked.description ?? "");
	if (!described) return undefined;
	const name = `${described[1]} ${described[2]}${described[3] ? " (1m context)" : ""}`.toLowerCase();
	const named = offered.filter((model) => agentModelDisplayLabel("claude-code", model.label).toLowerCase() === name);
	return named.find((model) => CLAUDE_FAMILIES.includes(model.id.replace(/\[.*\]$/, "").toLowerCase())) ?? named[0];
}

function claudeFamilyVersion(model: { id: string; label: string }) {
	for (const text of [modelChoiceLabel(model), model.id]) {
		const lower = text.toLowerCase().replace(/\(.*?\)|\[.*?\]/g, "");
		const family = CLAUDE_FAMILIES.findIndex((name) => lower.includes(name));
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

export function newestClaudeFamilyModel<T extends { id: string; label?: string; efforts?: string[] }>(models: T[], alias: string): T | undefined {
	const family = CLAUDE_FAMILIES.indexOf(alias.trim().replace(/\[1m\]$/, "").toLowerCase());
	if (family < 0) return undefined;
	let best: { model: T; version: number[] } | undefined;
	for (const model of models) {
		if (!model.efforts?.length) continue;
		const parsed = claudeFamilyVersion({ id: model.id, label: model.label ?? "" });
		if (parsed?.family === family && (!best || compareVersionsDesc(parsed.version, best.version) < 0)) {
			best = { model, version: parsed.version };
		}
	}
	return best?.model;
}

function claudeVariant(model: { id: string; label: string }): string {
	const variant = model.id.match(/\[(.*?)\]/)?.[1] ?? model.label.match(/\((.*?)\)/)?.[1] ?? "";
	return variant.toLowerCase().replace(/\s*context\s*$/, "").replace(/\s+/g, "");
}

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

export function mergeSameNamedModels<T extends { id: string; label: string; group?: string; aliases?: string[] } & ModelAvailability>(models: T[], keep?: string): T[] {
	return models.flatMap((model) => {
		const twins = models.filter((other) => other.label === model.label && other.group === model.group);
		if (twins.length === 1) return [model];
		const kept = twins.find((other) => keep && (other.id === keep || other.aliases?.includes(keep)))
			?? twins.find(isModelSelectable) ?? twins[0];
		if (model !== kept) return [];
		const aliases = [...new Set(twins.flatMap((other) => [...(other === kept ? [] : [other.id]), ...(other.aliases ?? [])]))]
			.filter((id) => id !== kept.id);
		return [{ ...kept, aliases }];
	});
}

export function claudeChoiceModel<T extends { id: string; label: string }>(catalog: T[]): ((value: string, name?: string) => T | undefined) | undefined {
	if (catalog.length === 0) return undefined;
	const newest = splitClaudeModels(catalog).current;
	return (value: string, name?: string) => {
		const id = value.toLowerCase();
		const exact = catalog.find((model) => model.id.toLowerCase() === id);
		if (exact) return exact;
		const named = name?.trim().toLowerCase();
		if (named && /\d/.test(named)) {
			return catalog.find((model) => agentModelDisplayLabel("claude-code", model.label).toLowerCase() === named);
		}
		const family = id.replace(/\[.*?\]$/, "");
		const variant = claudeVariant({ id, label: "" });
		return newest.find((model) => claudeVariant(model) === variant
			&& (model.id.toLowerCase().includes(`-${family}-`)
				|| agentModelDisplayLabel("claude-code", model.label).toLowerCase().startsWith(family)));
	};
}

type ModelAvailability = { requiresVersion?: string; unavailableReason?: string };

export function isModelSelectable(model: ModelAvailability): boolean {
	return !model.unavailableReason;
}

export function withoutLocalAvailability<T extends ModelAvailability>(models: T[]): T[] {
	if (!models.some((model) => model.requiresVersion || model.unavailableReason)) return models;
	return models.map((model) => (model.requiresVersion || model.unavailableReason
		? { ...model, requiresVersion: undefined, unavailableReason: undefined }
		: model));
}

export function foldClaudeAliasDefault<T extends { id: string; label: string; isDefault?: boolean } & ModelAvailability>(models: T[]): T[] {
	const alias = models.find((model) => model.isDefault && CLAUDE_FAMILIES.includes(model.id));
	if (!alias) return models;
	const rest = models.filter((model) => model !== alias);
	const family = CLAUDE_FAMILIES.indexOf(alias.id);
	const newest = splitClaudeModels(rest).current.find((model) => claudeFamilyVersion(model)?.family === family && claudeVariant(model) === "");
	return newest && isModelSelectable(newest) ? rest.map((model) => (model === newest ? { ...model, isDefault: true } : model)) : models;
}

export function recordedModelName(harness: string, model: string, models: Array<{ id: string; label: string }>): string {
	const id = model.replace(/\[.*$/, "");
	const label = models.find((entry) => entry.id === id)?.label
		?? (listsModelVariants(harness) ? collapseModelVariants(models).find((family) => family.id === id)?.label : undefined)
		?? (harness === "claude-code" ? splitClaudeModels(models).current.find((entry) => entry.id.includes(`-${id}-`))?.label : undefined);
	return agentModelDisplayLabel(harness, label ?? id);
}
