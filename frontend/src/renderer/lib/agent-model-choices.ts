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

function parseModelVersion(label: string): { family: string; version: number[] } | null {
	const match = /\d+(?:\.\d+)*/.exec(label);
	if (!match) return null;
	const family = label.slice(0, match.index).trim().toLowerCase();
	if (!family) return null;
	return { family, version: match[0].split(".").map(Number) };
}

function compareVersionsDesc(a: number[], b: number[]): number {
	for (let i = 0; i < Math.max(a.length, b.length); i += 1) {
		const diff = (b[i] ?? 0) - (a[i] ?? 0);
		if (diff !== 0) return diff;
	}
	return 0;
}

/** Newest version first within a model family. Families keep first-appearance order unless familyOrder ranks them; unversioned models keep their place. */
/** Claude Code lists its newest model per family in this order. */
export const CLAUDE_FAMILY_ORDER = ["fable", "opus", "sonnet", "haiku"] as const;

function familyRank(text: string, familyOrder: readonly string[]): number {
	const lower = text.toLowerCase();
	const rank = familyOrder.findIndex((family) => lower.includes(family));
	return rank === -1 ? familyOrder.length : rank;
}

export function sortModelsByFamilyVersion<T extends { id: string; label: string }>(
	models: T[],
	familyOrder: readonly string[] = [],
): T[] {
	const slots: (T | string)[] = [];
	const families = new Map<string, { model: T; version: number[] }[]>();
	for (const model of models) {
		const parsed = parseModelVersion(modelChoiceLabel(model));
		if (!parsed) {
			slots.push(model);
			continue;
		}
		const members = families.get(parsed.family);
		if (members) {
			members.push({ model, version: parsed.version });
		} else {
			families.set(parsed.family, [{ model, version: parsed.version }]);
			slots.push(parsed.family);
		}
	}
	const ordered = familyOrder.length
		? slots
				.map((slot, index) => ({
					slot,
					index,
					rank: familyRank(typeof slot === "string" ? slot : modelChoiceLabel(slot), familyOrder),
				}))
				.sort((a, b) => a.rank - b.rank || a.index - b.index)
				.map((entry) => entry.slot)
		: slots;
	return ordered.flatMap((slot) =>
		typeof slot === "string"
			? families
					.get(slot)!
					.sort((a, b) => compareVersionsDesc(a.version, b.version))
					.map((member) => member.model)
			: [slot],
	);
}

/** Ids of every model that is not the newest version of its family. Expects the output of sortModelsByFamilyVersion. */
export function legacyModelIDs<T extends { id: string; label: string }>(sorted: T[]): Set<string> {
	const seen = new Set<string>();
	const legacy = new Set<string>();
	for (const model of sorted) {
		const parsed = parseModelVersion(modelChoiceLabel(model));
		if (!parsed) continue;
		if (seen.has(parsed.family)) legacy.add(model.id);
		else seen.add(parsed.family);
	}
	return legacy;
}
