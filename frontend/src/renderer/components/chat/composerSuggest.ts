/**
 * The composer's suggestion logic, kept out of the component.
 *
 * Everything here is a pure function of (text, caret) plus the candidate lists, so
 * the parts that are easy to get wrong — where a trigger starts, what a selection
 * replaces, where the caret lands — are testable without a DOM or a provider.
 *
 * The editor renders accepted suggestions as atomic chips, but trigger matching
 * and ranking stay plain-text operations here. Keeping that policy independent
 * of Lexical makes the menu deterministic and lets the wire representation remain
 * ordinary text for the agent.
 */

import type { ChatSkill } from "../../types/conversation";

/** What kind of thing the caret is currently completing. */
export type TriggerKind = "skill" | "file";

/**
 * An in-progress trigger the caret sits inside.
 *
 * `start` is the index of the trigger character itself, so a replacement can
 * overwrite the sigil along with the query.
 */
export interface ActiveTrigger {
	kind: TriggerKind;
	/** Index of the `/` or `@`. */
	start: number;
	/** Text typed after the sigil, up to the caret. */
	query: string;
}

/**
 * Characters that end a trigger. A query cannot contain whitespace: once the user
 * types a space they have moved on to prose, and holding the menu open would make
 * the next Enter select a skill instead of sending the message.
 */
const TRIGGER_BOUNDARY = /\s/;

/**
 * Find the trigger the caret is inside, if any.
 *
 * A sigil only counts at the start of the text or after whitespace. Without that
 * rule an email address or a path like `src/app` would open a menu mid-word, and
 * `and/or` would hijack Enter. Both commands and file mentions can be added after
 * existing prose.
 */
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

/**
 * Score a candidate against a query. Lower is better; `null` means no match.
 *
 * The bands (prefix, word boundary, substring) are what make the ordering feel
 * intentional rather than alphabetical: typing "rev" should put `review` above
 * `code-review`, and both above something that only mentions review in its
 * description.
 */
function score(value: string, query: string, base: number): number | null {
	const haystack = value.toLowerCase();
	if (query === "") return base;
	if (haystack === query) return base;
	if (haystack.startsWith(query)) return base + 1;

	// A match after a separator reads as the start of a word to a person.
	for (const marker of ["-", "_", "/", ":", "."]) {
		if (haystack.includes(marker + query)) return base + 2;
	}
	const at = haystack.indexOf(query);
	if (at >= 0) return base + 3 + Math.min(at, 20) / 100;
	return null;
}

/** Word separators a person may or may not type. */
const LOOSE_SEPARATORS = /[-_\s]+/g;

/**
 * {@link score}, ignoring hyphens, underscores and spaces on both sides, so
 * `runaofast`, `run-ao-fast` and `run_ao` find the same skills in the same order.
 * The words are joined and their starts remembered, which keeps the
 * start-of-a-word band: `plan` still ranks `ce-plan` above `explanation`.
 */
function looseScore(value: string, query: string, base: number): number | null {
	const needle = query.replace(LOOSE_SEPARATORS, "");
	if (needle === "") return base;
	const lower = value.toLowerCase();
	// Most candidates do not match at all; rule them out natively before walking
	// a long description character by character on every keystroke.
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
		// After `/`, `:` or `.` the next character starts a word too.
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

/** One row in the suggestion menu. */
export interface Suggestion {
	/** Stable key, and the value a selection inserts. */
	value: string;
	/** The label shown. A `/` row shows the name without its slash or `skill:` prefix. */
	label: string;
	/** Secondary line, when there is something worth saying. */
	detail?: string;
	/** Right-aligned provenance, e.g. a skill's scope. */
	badge?: string;
}

/**
 * How many ranked rows the menu will hold.
 *
 * Set well above what fits so the panel's own scrollbar is what tells the user
 * there is more: a hard cap at the visible row count showed 8 of 99 installed
 * skills with nothing to suggest the rest existed. The cap is still there to stop a
 * five-thousand-file worktree from rendering itself into the menu.
 */
export const MAX_SUGGESTIONS = 50;

/**
 * Pi-style ACP agents list each skill as the command `skill:<name>`. The prefix
 * is how the agent is told to run it, not part of its name.
 */
const SKILL_COMMAND_PREFIX = "skill:";

/**
 * The name a skill command is shown by: `skill:Better Interface` reads as
 * `Better Interface`. Undefined for any other command. Display only; the command
 * name is still what is inserted and sent.
 */
export function skillCommandName(name: string): string | undefined {
	if (!name.startsWith(SKILL_COMMAND_PREFIX)) return undefined;
	const bare = name.slice(SKILL_COMMAND_PREFIX.length).trim();
	return bare === "" ? undefined : bare;
}

/** Words read as initialisms rather than capitalized: `better-ui` is "Better UI". */
const INITIALISMS = new Set([
	"ai", "api", "btw", "ce", "ci", "cli", "cso", "css", "dx", "gh", "html", "ide", "json", "llm", "mcp",
	"pdf", "pr", "prd", "qa", "sdk", "seo", "sql", "svg", "tdd", "ui", "url", "ux", "yaml",
]);
/** Words with their own casing. A Map, so a skill named `constructor` finds nothing inherited. */
const WORD_CASING = new Map([
	["github", "GitHub"],
	["ios", "iOS"],
	["macos", "macOS"],
]);

/**
 * A slug as a title: `ce-plan` is "CE Plan", `__remote-workflow` is "Remote
 * Workflow". A word the author already cased (`Better`, `iOS`) is kept as written.
 */
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

/**
 * How a listed entry reads in the menu: a name, not an invocation. A label the
 * provider or the skill's own metadata gave is used as written. Otherwise the
 * name is read as a title, without the `skill:` prefix or a plugin namespace
 * (shown quietly when it matters), so every agent's menu reads alike.
 */
function skillLabel(skill: ChatSkill): string {
	if (skill.displayName && skill.displayName !== skill.name) return skillCommandName(skill.displayName) ?? skill.displayName;
	const bare = skillCommandName(skill.name) ?? skill.name;
	// A malformed name such as `plugin:` has nothing after its namespace; show it whole.
	return titleFromSlug(bare.slice(bare.lastIndexOf(":") + 1)) || bare;
}

/**
 * The namespace in front of a skill's name (`plugin:skill`), if it has one. Shown
 * quietly only when two listed skills would otherwise read the same.
 */
function skillNamespace(name: string): string | undefined {
	const id = skillCommandName(name) ?? name;
	const colon = id.lastIndexOf(":");
	return colon > 0 ? id.slice(0, colon) : undefined;
}

/**
 * Rank skills for a `/` query.
 *
 * Matching runs over the invocable name, the label, and the description — a user
 * who remembers what a skill does but not what it is called still finds it — but
 * a name match always outranks a description match.
 */
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
			// Only searched when the user typed something; an empty query must not be
			// ranked by description text.
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

	// Name is the tie-break so the order is stable across renders: two skills with
	// the same score must not swap places as the list is refetched.
	scored.sort((a, b) => a.score - b.score || a.name.localeCompare(b.name));
	return scored.slice(0, MAX_SUGGESTIONS).map((entry) => entry.suggestion);
}

function skillDetail(skill: ChatSkill): string | undefined {
	const description = summary(skill.description);
	const hint = firstLine(skill.inputHint);
	if (description && hint) return `${description} · ${hint}`;
	return description || hint;
}

/**
 * Rank worktree paths for an `@` query.
 *
 * The basename is scored ahead of the full path: people search for `ChatComposer`,
 * not for the directories above it. The full path is still matched so a directory
 * fragment narrows the list.
 */
export function rankFiles(paths: readonly string[], query: string): Suggestion[] {
	const needle = query.trim().toLowerCase();
	const scored: { suggestion: Suggestion; score: number; path: string }[] = [];

	for (const path of paths) {
		if (!path) continue;
		// Split explicitly rather than with slice(lastIndexOf(...)): a file at the repo
		// root has no slash, and the negative index silently truncates its own name
		// into a parent directory that does not exist.
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
			// The row reads as "name, then where it lives", but the value inserted is
			// always the whole path — the label is never what the agent resolves.
			suggestion: { value: path, label: base, detail: parent || undefined },
		});
	}

	// Shorter paths first among equals: a file at the repo root is more often what
	// was meant than one buried six directories deep.
	scored.sort(
		(a, b) =>
			a.score - b.score || a.path.length - b.path.length || a.path.localeCompare(b.path),
	);
	return scored.slice(0, MAX_SUGGESTIONS).map((entry) => entry.suggestion);
}

/**
 * A description cut to what a one-line row can say. Skill descriptions are
 * written for the model and run on ("…. Use when the user asks …"); the first
 * sentence is the summary. Claude also appends where a skill came from, such
 * as "(user)", which the row does not need. Other trailing parentheses, such as
 * "(PDF)", are part of the description and stay.
 */
function summary(text: string | undefined): string | undefined {
	const line = firstLine(text)?.replace(PROVENANCE_TAGS, "");
	if (!line) return undefined;
	// A sentence ends where the next one starts with a capital, so "e.g. this" stays whole.
	const sentence = line.split(/(?<=[\w)\]`'"][.!?])\s+(?=[A-Z(`"'])/, 1)[0]!;
	return sentence.replace(/(?<!\.)\.$/, "") || undefined;
}

/** Claude's trailing source tags: "(user)", "(project)", "(plugin)" and the like. */
const PROVENANCE_TAGS = /(?:\s*\((?:user|project|local|managed|plugin|builtin|bundled|policy)\))+$/i;

function firstLine(text: string | undefined): string | undefined {
	if (!text) return undefined;
	const line = text.split("\n", 1)[0]!.trim();
	return line === "" ? undefined : line;
}

/**
 * Move the highlighted row, wrapping at both ends.
 *
 * Wrapping matters for a short list reached by keyboard: pressing Up on the first
 * row should reach the last one rather than doing nothing.
 */
export function moveHighlight(current: number, delta: number, count: number): number {
	if (count === 0) return 0;
	return (current + delta + count) % count;
}
