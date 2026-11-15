import type { ChatSkill } from "../../types/conversation";

export type TriggerKind = "skill" | "file";

export interface ActiveTrigger {
	kind: TriggerKind;
	start: number;
	query: string;
}

const TRIGGER_BOUNDARY = /\s/;

export function findActiveTrigger(text: string, caret: number): ActiveTrigger | undefined {
	for (let i = caret - 1; i >= 0; i -= 1) {
		const char = text[i]!;
		if (TRIGGER_BOUNDARY.test(char)) return undefined;
		if (char !== "/" && char !== "@") continue;

		const preceding = i > 0 ? text[i - 1]! : undefined;
		const atBoundary = preceding === undefined || TRIGGER_BOUNDARY.test(preceding);
		if (!atBoundary) return undefined;

		return {
			kind: char === "/" ? "skill" : "file",
			start: i,
			query: text.slice(i + 1, caret),
		};
	}
	return undefined;
}

function score(value: string, query: string, base: number): number | null {
	const haystack = value.toLowerCase();
	if (query === "") return base;
	if (haystack === query) return base;
	if (haystack.startsWith(query)) return base + 1;

	for (const marker of ["-", "_", "/", ":", "."]) {
		if (haystack.includes(marker + query)) return base + 2;
	}
	const at = haystack.indexOf(query);
	if (at >= 0) return base + 3 + Math.min(at, 20) / 100;
	return null;
}

const LOOSE_SEPARATORS = /[-_\s]+/g;

function looseScore(value: string, query: string, base: number): number | null {
	const needle = query.replace(LOOSE_SEPARATORS, "");
	if (needle === "") return base;
	const lower = value.toLowerCase();
	if (!lower.replace(LOOSE_SEPARATORS, "").includes(needle)) return null;
	let joined = "";
	const wordStarts: number[] = [];
	let atWordStart = true;
	for (const char of lower) {
		if (char === "-" || char === "_" || /\s/.test(char)) {
			atWordStart = true;
			continue;
		}
		if (atWordStart) wordStarts.push(joined.length);
		atWordStart = char === "/" || char === ":" || char === ".";
		joined += char;
	}
	if (joined === needle) return base;
	if (joined.startsWith(needle)) return base + 1;
	if (wordStarts.some((start) => start > 0 && joined.startsWith(needle, start))) return base + 2;
	const at = joined.indexOf(needle);
	if (at >= 0) return base + 3 + Math.min(at, 20) / 100;
	return null;
}

export interface Suggestion {
	value: string;
	label: string;
	detail?: string;
	badge?: string;
}

export const MAX_SUGGESTIONS = 50;

const SKILL_COMMAND_PREFIX = "skill:";

export function skillCommandName(name: string): string | undefined {
	if (!name.startsWith(SKILL_COMMAND_PREFIX)) return undefined;
	const bare = name.slice(SKILL_COMMAND_PREFIX.length).trim();
	return bare === "" ? undefined : bare;
}

const INITIALISMS = new Set([
	"ai", "api", "btw", "ce", "ci", "cli", "cso", "css", "dx", "gh", "html", "ide", "json", "llm", "mcp",
	"pdf", "pr", "prd", "qa", "sdk", "seo", "sql", "svg", "tdd", "ui", "url", "ux", "yaml",
]);
const WORD_CASING = new Map([
	["github", "GitHub"],
	["ios", "iOS"],
	["macos", "macOS"],
]);

function titleFromSlug(slug: string): string {
	const words = slug.split(/[-_\s]+/).filter(Boolean);
	if (words.length === 0) return slug;
	return words
		.map((word) => {
			if (/[A-Z]/.test(word)) return word;
			const lower = word.toLowerCase();
			const cased = WORD_CASING.get(lower);
			if (cased) return cased;
			if (INITIALISMS.has(lower)) return lower.toUpperCase();
			return lower.charAt(0).toUpperCase() + lower.slice(1);
		})
		.join(" ");
}

function skillLabel(skill: ChatSkill): string {
	if (skill.displayName && skill.displayName !== skill.name) return skillCommandName(skill.displayName) ?? skill.displayName;
	const bare = skillCommandName(skill.name) ?? skill.name;
	return titleFromSlug(bare.slice(bare.lastIndexOf(":") + 1)) || bare;
}

function skillNamespace(name: string): string | undefined {
	const id = skillCommandName(name) ?? name;
	const colon = id.lastIndexOf(":");
	return colon > 0 ? id.slice(0, colon) : undefined;
}

export function rankSkills(skills: readonly ChatSkill[], query: string): Suggestion[] {
	const needle = query.trim().toLowerCase();
	const scored: { suggestion: Suggestion; score: number; name: string }[] = [];

	const labelCounts = new Map<string, number>();
	for (const skill of skills) {
		if (!skill.name) continue;
		const label = skillLabel(skill);
		labelCounts.set(label, (labelCounts.get(label) ?? 0) + 1);
	}

	for (const skill of skills) {
		if (!skill.name) continue;
		const candidates = [
			looseScore(skill.name, needle, 0),
			looseScore(skill.displayName || skill.name, needle, 10),
			needle === "" ? null : looseScore(skill.description ?? "", needle, 40),
			needle === "" ? null : looseScore(skill.inputHint ?? "", needle, 50),
		].filter((value): value is number => value !== null);
		if (candidates.length === 0) continue;

		const label = skillLabel(skill);
		const namespace = (labelCounts.get(label) ?? 0) > 1 ? skillNamespace(skill.name) : undefined;
		scored.push({
			score: Math.min(...candidates),
			name: skill.name,
			suggestion: {
				value: skill.name,
				label,
				detail: skillDetail(skill),
				badge: namespace ?? skill.source,
			},
		});
	}

	scored.sort((a, b) => a.score - b.score || a.name.localeCompare(b.name));
	return scored.slice(0, MAX_SUGGESTIONS).map((entry) => entry.suggestion);
}

function skillDetail(skill: ChatSkill): string | undefined {
	const description = summary(skill.description);
	const hint = firstLine(skill.inputHint);
	if (description && hint) return `${description} · ${hint}`;
	return description || hint;
}

export function rankFiles(paths: readonly string[], query: string): Suggestion[] {
	const needle = query.trim().toLowerCase();
	const scored: { suggestion: Suggestion; score: number; path: string }[] = [];

	for (const path of paths) {
		if (!path) continue;
		const slash = path.lastIndexOf("/");
		const base = slash < 0 ? path : path.slice(slash + 1);
		const parent = slash < 0 ? "" : path.slice(0, slash);

		const candidates = [score(base, needle, 0), score(path, needle, 10)].filter(
			(value): value is number => value !== null,
		);
		if (candidates.length === 0) continue;

		scored.push({
			score: Math.min(...candidates),
			path,
			suggestion: { value: path, label: base, detail: parent || undefined },
		});
	}

	scored.sort(
		(a, b) =>
			a.score - b.score || a.path.length - b.path.length || a.path.localeCompare(b.path),
	);
	return scored.slice(0, MAX_SUGGESTIONS).map((entry) => entry.suggestion);
}

function summary(text: string | undefined): string | undefined {
	const line = firstLine(text)?.replace(PROVENANCE_TAGS, "");
	if (!line) return undefined;
	const sentence = line.split(/(?<=[\w)\]`'"][.!?])\s+(?=[A-Z(`"'])/, 1)[0]!;
	return sentence.replace(/(?<!\.)\.$/, "") || undefined;
}

const PROVENANCE_TAGS = /(?:\s*\((?:user|project|local|managed|plugin|builtin|bundled|policy)\))+$/i;

function firstLine(text: string | undefined): string | undefined {
	if (!text) return undefined;
	const line = text.split("\n", 1)[0]!.trim();
	return line === "" ? undefined : line;
}

export function moveHighlight(current: number, delta: number, count: number): number {
	if (count === 0) return 0;
	return (current + delta + count) % count;
}
