/**
 * Cursor lists one entry per model, effort, thinking and speed ("Claude Opus 4.7 1M High
 * Thinking Fast" and ~250 siblings). Pickers show each model once with the settings as
 * controls; the variant id is still what is sent to the agent.
 */

export interface ModelVariant {
	id: string;
	/** Absent when the family has no effort levels. */
	effort?: string;
	thinking: boolean;
	fast: boolean;
}

export interface ModelFamily {
	/** The variant id with its setting suffixes removed. */
	id: string;
	label: string;
	group: string;
	variants: ModelVariant[];
	/** The variant the agent itself lists without qualifiers. */
	defaultVariant: ModelVariant;
	/** Effort levels this family offers, lowest first. */
	efforts: string[];
	hasFast: boolean;
	hasThinkingChoice: boolean;
}

const EFFORT_ORDER = ["none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"];
/** A setting word at the end of a label. The two-word ones come first so they are taken whole. */
const LABEL_SUFFIX = / (no thinking|extra high|thinking|fast|high|medium|low|max|none|minimal)$/i;
const VENDORS: Array<[RegExp, string]> = [
	[/^claude\b/i, "Anthropic"],
	[/^(gpt|codex|o\d)\b/i, "OpenAI"],
	[/^gemini\b/i, "Google"],
	[/^grok\b/i, "xAI"],
	[/^(composer|auto)\b/i, "Cursor"],
	[/^glm\b/i, "Z.ai"],
	[/^kimi\b/i, "Moonshot"],
	[/^muse\b/i, "Meta"],
	[/^deepseek\b/i, "DeepSeek"],
	[/^qwen\b/i, "Alibaba"],
];

/** Agents whose catalogs fold settings into the model id. */
export function listsModelVariants(harness: string | undefined): boolean {
	return harness === "cursor";
}

/** The vendor a model belongs to, read from the start of its name. */
function modelVendor(label: string): string {
	const trimmed = label.trim();
	return VENDORS.find(([pattern]) => pattern.test(trimmed))?.[1] ?? trimmed.split(/\s+/)[0] ?? "";
}

/** Models grouped by vendor, each group where its first model was. */
export function groupModelsByVendor<T extends { label: string; group?: string }>(models: T[]): (T & { group: string })[] {
	const grouped = models.map((model) => ({ ...model, group: model.group || modelVendor(model.label) }));
	const order = [...new Set(grouped.map((model) => model.group))];
	return grouped.sort((a, b) => order.indexOf(a.group) - order.indexOf(b.group));
}

function parseVariantId(id: string): { base: string; effort?: string; thinking: boolean; fast: boolean } {
	const tokens = id.split("-");
	let effort: string | undefined;
	let thinking = false;
	let fast = false;
	while (tokens.length > 1) {
		const last = tokens[tokens.length - 1].toLowerCase();
		if (last === "fast" && !fast) {
			fast = true;
		} else if (last === "thinking" && !thinking) {
			thinking = true;
		} else if (effort === undefined && last === "high" && tokens[tokens.length - 2]?.toLowerCase() === "extra") {
			effort = "xhigh";
			tokens.pop();
		} else if (effort === undefined && EFFORT_ORDER.includes(last) &&
			// "codex-max" is a model of its own, not GPT Codex at Max effort.
			!(last === "max" && tokens[tokens.length - 2]?.toLowerCase() === "codex")) {
			effort = last;
		} else {
			break;
		}
		tokens.pop();
	}
	return { base: tokens.join("-"), effort, thinking, fast };
}

function cleanLabel(label: string): string {
	return label.replace(/[​-‍﻿]/g, "").replace(/\b1M\b/g, "").replace(/\s+/g, " ").trim();
}

function baseLabel(label: string): string {
	let text = cleanLabel(label);
	// "Codex Max" is a model of its own, not Codex at Max effort.
	while (LABEL_SUFFIX.test(text) && !/codex max$/i.test(text)) text = text.replace(LABEL_SUFFIX, "").trim();
	return text;
}

/** Folds a variant-per-setting catalog into one entry per model. Order is kept. */
export function collapseModelVariants(models: Array<{ id: string; label: string }>): ModelFamily[] {
	const families = new Map<string, { labels: string[]; plain?: string; variants: ModelVariant[] }>();
	for (const model of models) {
		const parsed = parseVariantId(model.id);
		const family = families.get(parsed.base) ?? { labels: [], variants: [] };
		families.set(parsed.base, family);
		const label = baseLabel(model.label);
		family.labels.push(label);
		// The entry the agent lists with no qualifier is the model's own default.
		if (cleanLabel(model.label) === label && !parsed.fast) family.plain ??= model.id;
		family.variants.push({ id: model.id, effort: parsed.effort, thinking: parsed.thinking, fast: parsed.fast });
	}
	return [...families.entries()].map(([id, family]) => {
		const explicit = new Set(family.variants.map((variant) => variant.effort).filter(Boolean));
		// A variant with no effort in its id sits at the level its siblings leave free.
		const implied = explicit.size > 0 && !explicit.has("medium") ? "medium" : undefined;
		const variants = family.variants.map((variant) => ({ ...variant, effort: variant.effort ?? implied }));
		const label = family.labels.reduce((shortest, next) => (next.length < shortest.length ? next : shortest));
		const efforts = EFFORT_ORDER.filter((effort) => variants.some((variant) => variant.effort === effort));
		const defaultVariant =
			variants.find((variant) => variant.id === family.plain) ??
			variants.find((variant) => !variant.fast && variant.effort === "high") ??
			variants.find((variant) => !variant.fast && variant.effort === "medium") ??
			variants.find((variant) => !variant.fast) ??
			variants[0];
		return {
			id,
			label,
			group: modelVendor(label),
			variants,
			defaultVariant,
			efforts,
			hasFast: variants.some((variant) => variant.fast),
			hasThinkingChoice: variants.some((variant) => variant.thinking) && variants.some((variant) => !variant.thinking),
		};
	});
}

/** The family and variant a variant id belongs to. */
export function findModelVariant(families: ModelFamily[], variantId: string | undefined) {
	if (!variantId) return undefined;
	for (const family of families) {
		const variant = family.variants.find((candidate) => candidate.id === variantId);
		if (variant) return { family, variant };
	}
	return undefined;
}

/** The family's variant closest to the wanted settings: effort over thinking over speed, as not every combination exists. */
export function pickModelVariant(
	family: ModelFamily,
	want: { effort?: string; thinking?: boolean; fast?: boolean },
): ModelVariant {
	const byEffort = family.variants.filter((variant) => want.effort === undefined || variant.effort === want.effort);
	const pool = byEffort.length > 0 ? byEffort : family.variants;
	const byThinking = pool.filter((variant) => want.thinking === undefined || variant.thinking === want.thinking);
	const narrowed = byThinking.length > 0 ? byThinking : pool;
	return narrowed.find((variant) => variant.fast === Boolean(want.fast)) ?? narrowed[0] ?? family.defaultVariant;
}

const UPPERCASE_WORDS = new Set(["gpt", "glm", "ai", "api"]);
const GROUP_NAMES: Record<string, string> = { openai: "OpenAI", xai: "xAI", deepseek: "DeepSeek", "z.ai": "Z.ai" };

/** A readable name for a model listed only by id ("gpt-5.3-codex-spark" is "GPT-5.3 Codex Spark"); written-out labels are kept. */
export function readableModelLabel(label: string): string {
	// A routed id ("openrouter/anthropic/claude-haiku-4.5") is named by its last part.
	label = label.slice(label.lastIndexOf("/") + 1);
	if (!label || /[A-Z\s]/.test(label)) return label;
	const words: string[] = [];
	for (const part of label.split("-")) {
		const previous = words[words.length - 1];
		if (/^\d/.test(part) && previous && UPPERCASE_WORDS.has(previous.toLowerCase())) {
			words[words.length - 1] = `${previous}-${part}`;
		} else {
			words.push(UPPERCASE_WORDS.has(part) ? part.toUpperCase() : part.charAt(0).toUpperCase() + part.slice(1));
		}
	}
	return words.join(" ");
}

/** A running agent's "<provider>/<model>" choice (Pi's chat) named as its catalog shows it, under its provider. */
export function providerQualifiedChoice<T extends { value: string; name: string; group?: string; groupName?: string }>(choice: T, harness: string): T {
	const slash = choice.value.indexOf("/");
	if (slash <= 0 || choice.group || choice.groupName) return choice;
	const provider = choice.value.slice(0, slash);
	if (!choice.name.toLowerCase().startsWith(`${provider.toLowerCase()}/`)) return choice;
	return { ...choice, name: readableModelLabel(choice.name.slice(slash + 1)), group: modelGroupLabel(provider, harness) };
}

/**
 * The heading for models listed under a provider id: "openai-codex" becomes "OpenAI Codex".
 * An agent's own id yields none: Kimi's "managed:kimi-code" is not shown.
 */
export function modelGroupLabel(group: string | undefined, harness: string): string | undefined {
	const raw = (group ?? "").trim();
	if (!raw) return undefined;
	const id = raw.slice(raw.lastIndexOf(":") + 1).toLowerCase();
	if (!id || id.startsWith("[")) return undefined;
	const own = harness.toLowerCase().replace(/[^a-z0-9]/g, "");
	if (own && id.replace(/[^a-z0-9]/g, "").includes(own)) return undefined;
	// Vendor names from a variant catalog are already written out.
	if (/[A-Z]/.test(raw)) return raw;
	return id.split(/[-_\s]+/).map((word) => GROUP_NAMES[word] ?? word.charAt(0).toUpperCase() + word.slice(1)).join(" ");
}
