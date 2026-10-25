// Name matching for the command palette: actions, settings, sessions, chats,
// projects and PRs are matched by name in memory, on the same score scale the
// daemon's /api/v1/search uses for file and chat content, so one ranking can
// mix them. The weights mirror the measured tuning in the search design doc;
// change them only together with palette-search.test.ts.

export type NameKind = "action" | "setting" | "session" | "chat" | "project" | "pr" | "other";

export type NameTarget = {
	title: string;
	/** Synonyms and identifiers that should also find this target, at reduced weight. */
	keywords?: readonly string[];
	kind: NameKind;
};

export type NameMatch = {
	score: number;
	/** IDF-weighted share of the query this name covers, 0..1. */
	coverage: number;
};

const EXACT_NAME = 6.0;
const COVERAGE = 3.0;
const IN_ORDER = 1.0;
const ABBREVIATION = 2.4;
const KEYWORD_SHARE = 0.7;
const MIN_SCORE = 0.9;

/**
 * When the best name covers at least this share of the query, it wins Enter:
 * content rows still list, but only decisive content (a code-like or quoted
 * query, an exact phrase of three or more words, a full file name) may
 * outrank it. Without this rule a file that merely mentions "dark mode" beat
 * the Theme setting.
 */
export const NAMES_FIRST_COVERAGE = 0.6;

const KIND_EDGE: Record<NameKind, number> = {
	action: 1.15,
	setting: 1.1,
	session: 1.1,
	project: 1.1,
	pr: 1.1,
	chat: 1.05,
	other: 1,
};

const STOP_WORDS = new Set(
	"a an and are as at be by do does did for from how i in is it of on or the this that to we where what when which who why with our us you your can should get there here instead into out me my was were has have had about any some also just then so not no yes please pls".split(" "),
);

/** Words describing what to do to a control, not the control itself. */
const CONTROL_FILLER = new Set("turn off on enable disable change set make my another other way want need please".split(" "));

export function splitNameWords(text: string): string[] {
	return text
		.toLowerCase()
		.replace(/[…#()]/g, " ")
		.split(/[^\p{L}\p{N}]+/u)
		.filter(Boolean);
}

/**
 * A light English stemmer, enough for "closing"/"close", "builds"/"build",
 * "settings"/"setting": drop a plural s, then -ing or -ed, then a final e.
 */
export function stemWord(word: string): string {
	let s = word;
	if (s.length > 3 && s.endsWith("s") && !s.endsWith("ss")) s = s.slice(0, -1);
	if (s.length > 5 && s.endsWith("ing")) s = s.slice(0, -3);
	else if (s.length > 4 && s.endsWith("ed")) s = s.slice(0, -2);
	if (s.length > 3 && s.endsWith("e")) s = s.slice(0, -1);
	return s;
}

export function withinOneEdit(a: string, b: string): boolean {
	if (a === b) return true;
	const la = a.length;
	const lb = b.length;
	if (Math.abs(la - lb) > 1) return false;
	let i = 0;
	while (i < la && i < lb && a[i] === b[i]) i++;
	if (la === lb) {
		if (i + 1 < la && a[i] === b[i + 1] && a[i + 1] === b[i] && a.slice(i + 2) === b.slice(i + 2)) return true;
		return a.slice(i + 1) === b.slice(i + 1);
	}
	return la > lb ? a.slice(i + 1) === b.slice(i) : a.slice(i) === b.slice(i + 1);
}

export function withinTwoEdits(a: string, b: string): boolean {
	if (Math.abs(a.length - b.length) > 2) return false;
	let prev2: number[] = new Array(b.length + 1).fill(0);
	let prev: number[] = Array.from({ length: b.length + 1 }, (_, j) => j);
	let cur: number[] = new Array(b.length + 1).fill(0);
	for (let i = 1; i <= a.length; i++) {
		cur[0] = i;
		let rowMin = cur[0];
		for (let j = 1; j <= b.length; j++) {
			const cost = a[i - 1] === b[j - 1] ? 0 : 1;
			cur[j] = Math.min(prev[j]! + 1, cur[j - 1]! + 1, prev[j - 1]! + cost);
			if (i > 1 && j > 1 && a[i - 1] === b[j - 2] && a[i - 2] === b[j - 1]) cur[j] = Math.min(cur[j]!, prev2[j - 2]! + 1);
			rowMin = Math.min(rowMin, cur[j]!);
		}
		if (rowMin > 2) return false;
		[prev2, prev, cur] = [prev, cur, prev2];
	}
	return prev[b.length]! <= 2;
}

/** How well one query word matches one name word. */
function wordCredit(query: string, word: string): number {
	if (query === word) return 1;
	if (query.length >= 2 && word.startsWith(query)) return 0.9;
	if (stemWord(query) === stemWord(word)) return 0.85;
	// Four-letter words sit one edit from too many others ("mode"/"move").
	if (query.length >= 5 && withinOneEdit(query, word)) return 0.65;
	if (query.length >= 7 && withinTwoEdits(query, word)) return 0.5;
	return 0;
}

// --- fzf-style abbreviation matching ("tgl term" → Toggle terminal) ---

const BOUNDARY = 10;
const CONSECUTIVE = 8;
const MATCH = 16;
const GAP_START = -3;
const GAP_EXTEND = -1;
const NEG = -(1 << 30);

/** Best placement of query's letters in text (both lowercase, no spaces). */
export function abbreviationScore(query: string, text: string): number | null {
	if (!query) return 0;
	let qi = 0;
	for (let i = 0; i < text.length && qi < query.length; i++) if (text[i] === query[qi]) qi++;
	if (qi < query.length) return null;
	let prev = new Array<number>(text.length).fill(NEG);
	let cur = new Array<number>(text.length).fill(NEG);
	for (let i = 0; i < query.length; i++) {
		let carried = NEG;
		for (let j = 0; j < text.length; j++) {
			cur[j] = NEG;
			if (i > 0 && j >= 2 && prev[j - 2]! > NEG) carried = Math.max(carried, prev[j - 2]! + GAP_START);
			if (text[j] !== query[i]) {
				if (carried > NEG) carried += GAP_EXTEND;
				continue;
			}
			const bonus = j === 0 ? BOUNDARY : 0;
			let s = NEG;
			if (i === 0) s = MATCH + bonus * 2;
			else {
				if (j >= 1 && prev[j - 1]! > NEG) s = prev[j - 1]! + MATCH + Math.max(bonus, CONSECUTIVE);
				if (carried > NEG) s = Math.max(s, carried + MATCH + bonus);
			}
			cur[j] = s;
			if (carried > NEG) carried += GAP_EXTEND;
		}
		[prev, cur] = [cur, prev];
	}
	const top = Math.max(...prev);
	return top === NEG ? null : top;
}

// --- the name index: word rarity across every target ---

export type NameIndex = { docFreq: Map<string, number>; size: number };

export function buildNameIndex(targets: readonly NameTarget[]): NameIndex {
	const docFreq = new Map<string, number>();
	for (const target of targets) {
		const seen = new Set<string>();
		for (const word of splitNameWords([target.title, ...(target.keywords ?? [])].join(" "))) {
			const stem = stemWord(word);
			if (seen.has(stem)) continue;
			seen.add(stem);
			docFreq.set(stem, (docFreq.get(stem) ?? 0) + 1);
		}
	}
	return { docFreq, size: targets.length };
}

function wordWeight(word: string, index: NameIndex): number {
	if (CONTROL_FILLER.has(word)) return 0.3;
	const df = index.docFreq.get(stemWord(word)) ?? 0;
	return Math.log(1 + index.size / (1 + df));
}

function queryWords(query: string): string[] {
	const words = splitNameWords(query);
	return words.length <= 2 ? words : words.filter((w) => !STOP_WORDS.has(w));
}

/** Scores query against one target's name. Returns score 0 when it does not match. */
export function matchName(query: string, target: NameTarget, index: NameIndex): NameMatch {
	const words = queryWords(query);
	if (words.length === 0) return { score: 0, coverage: 0 };
	const normalizedQuery = splitNameWords(query).join(" ");
	const titleWords = splitNameWords(target.title);
	const keywordWords = splitNameWords((target.keywords ?? []).join(" "));

	let titleSum = 0;
	let mixedSum = 0;
	let weightSum = 0;
	let lastPosition = -1;
	let inOrder = true;
	for (const word of words) {
		const weight = wordWeight(word, index);
		weightSum += weight;
		let best = 0;
		let bestPosition = -1;
		titleWords.forEach((titleWord, position) => {
			const credit = wordCredit(word, titleWord);
			if (credit > best) {
				best = credit;
				bestPosition = position;
			}
		});
		if (best > 0) {
			if (bestPosition < lastPosition) inOrder = false;
			lastPosition = bestPosition;
		}
		titleSum += best * weight;
		let mixed = best;
		for (const keyword of keywordWords) mixed = Math.max(mixed, KEYWORD_SHARE * wordCredit(word, keyword));
		mixedSum += mixed * weight;
	}
	const titleCoverage = weightSum > 0 ? titleSum / weightSum : 0;
	const coverage = Math.max(titleCoverage, weightSum > 0 ? mixedSum / weightSum : 0);

	let score = 0;
	if (normalizedQuery === titleWords.join(" ")) score += EXACT_NAME;
	score += COVERAGE * coverage;
	if (titleCoverage >= 0.99 && inOrder) {
		score += IN_ORDER + (0.3 * words.length) / Math.max(titleWords.length, 1);
	}
	if (coverage < 0.6) {
		const joined = words.join("");
		const self = abbreviationScore(joined, joined);
		const fuzzy = abbreviationScore(joined, titleWords.join(""));
		if (self && fuzzy !== null) score = Math.max(score, ABBREVIATION * Math.min(1, fuzzy / self));
	}
	score *= KIND_EDGE[target.kind];
	return score >= MIN_SCORE ? { score, coverage } : { score: 0, coverage: 0 };
}

export type RankedEntry<T> = {
	item: T;
	score: number;
	/** Name coverage for name rows; undefined for content rows. */
	coverage?: number;
	/** Content rows only: evidence strong enough to outrank a matching name. */
	decisive?: boolean;
};

/**
 * Names first: once a name covers NAMES_FIRST_COVERAGE of the query, content
 * rows without decisive evidence are capped just below it. They still list;
 * they no longer take the Enter target from the control the user named.
 */
export function applyNamesFirst<T>(entries: RankedEntry<T>[]): RankedEntry<T>[] {
	let bestName: RankedEntry<T> | undefined;
	for (const entry of entries) {
		if (entry.coverage === undefined || entry.score <= 0) continue;
		if (!bestName || entry.score > bestName.score) bestName = entry;
	}
	if (!bestName || (bestName.coverage ?? 0) < NAMES_FIRST_COVERAGE) return entries;
	const ceiling = bestName.score * 0.99;
	return entries.map((entry) =>
		entry.coverage === undefined && !entry.decisive && entry.score > ceiling ? { ...entry, score: ceiling } : entry,
	);
}
