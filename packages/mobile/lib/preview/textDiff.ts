export type TextEdit =
	| { kind: "text"; value: string }
	| { kind: "key"; key: "Backspace" | "ArrowLeft" | "ArrowRight"; code: "Backspace" | "ArrowLeft" | "ArrowRight" };

const graphemes = typeof Intl !== "undefined" && "Segmenter" in Intl
	? new Intl.Segmenter(undefined, { granularity: "grapheme" })
	: null;

// The page's field moves its caret and deletes by whole characters, so the
// diff counts in characters too, never splitting an emoji or an accent.
function characters(text: string): string[] {
	return graphemes ? Array.from(graphemes.segment(text), (part) => part.segment) : Array.from(text);
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
