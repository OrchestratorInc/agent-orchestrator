export type TextEdit =
	| { kind: "text"; value: string }
	| { kind: "key"; key: "Backspace" | "ArrowLeft" | "ArrowRight"; code: "Backspace" | "ArrowLeft" | "ArrowRight" };

const graphemes = typeof Intl !== "undefined" && "Segmenter" in Intl
	? new Intl.Segmenter(undefined, { granularity: "grapheme" })
	: null;

// The page's field moves its caret and deletes by whole characters, so the
// diff counts in characters too, never splitting an emoji or an accent.
function characters(text: string): string[] {
	return graphemes ? Array.from(graphemes.segment(text), (part) => part.segment) : fallbackCharacters(text);
}

const ZWJ = 0x200d;
const isRegionalIndicator = (cp: number) => cp >= 0x1f1e6 && cp <= 0x1f1ff;
// Joins the code point before it: combining marks, variation selectors, skin
// tones, the keycap mark and emoji tag characters.
const extendsCharacter = (cp: number) =>
	(cp >= 0x0300 && cp <= 0x036f) ||
	(cp >= 0x1ab0 && cp <= 0x1aff) ||
	(cp >= 0x1dc0 && cp <= 0x1dff) ||
	(cp >= 0x20d0 && cp <= 0x20ff) ||
	(cp >= 0xfe00 && cp <= 0xfe0f) ||
	(cp >= 0xfe20 && cp <= 0xfe2f) ||
	(cp >= 0x1f3fb && cp <= 0x1f3ff) ||
	(cp >= 0xe0020 && cp <= 0xe007f) ||
	cp === ZWJ;

/**
 * Whole characters for engines without Intl.Segmenter (Hermes may lack it):
 * the common grapheme rules for accents, emoji sequences and flags, never a
 * lone code point of a joined emoji.
 */
export function fallbackCharacters(text: string): string[] {
	const out: string[] = [];
	let previous = -1;
	let flagHalf = false;
	for (const char of text) {
		const cp = char.codePointAt(0)!;
		const joins: boolean = out.length > 0 && (
			extendsCharacter(cp) ||
			previous === ZWJ ||
			(isRegionalIndicator(cp) && flagHalf)
		);
		if (joins) out[out.length - 1] += char;
		else out.push(char);
		flagHalf = isRegionalIndicator(cp) ? !(joins && flagHalf) : false;
		previous = cp;
	}
	return out;
}

const key = (name: "Backspace" | "ArrowLeft" | "ArrowRight"): TextEdit => ({ kind: "key", key: name, code: name });

/**
 * The fewest edits that turn the page's field from `prev` into `next`, for a
 * field whose caret sits at the end of `prev`, as it does while the phone types
 * into it. A change in the middle walks the caret there and back again.
 */
export function textEdits(prev: string, next: string): TextEdit[] {
	if (prev === next) return [];
	const before = characters(prev);
	const after = characters(next);
	let start = 0;
	while (start < before.length && start < after.length && before[start] === after[start]) start++;
	let end = 0;
	while (
		end < before.length - start &&
		end < after.length - start &&
		before[before.length - 1 - end] === after[after.length - 1 - end]
	) end++;
	const removed = before.length - start - end;
	const inserted = after.slice(start, after.length - end).join("");
	const edits: TextEdit[] = [];
	for (let i = 0; i < end; i++) edits.push(key("ArrowLeft"));
	for (let i = 0; i < removed; i++) edits.push(key("Backspace"));
	if (inserted) edits.push({ kind: "text", value: inserted });
	for (let i = 0; i < end; i++) edits.push(key("ArrowRight"));
	return edits;
}
