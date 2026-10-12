import type { Element, ElementContent, Root } from "hast";

/**
 * Wraps each streamed character in `<span class="stream-char">` so CSS can fade
 * it in as it mounts. Only the trailing characters are new elements on each
 * render (react-markdown keys by position), so earlier ones never replay the
 * animation. Apply only while streaming; a settled reply drops the plugin and
 * renders plain markdown again, instead of keeping a node per character.
 *
 * Bounds, so a long reply cannot make every update expensive or unsafe:
 *   - only the last block is wrapped — text streams in at the tail, and earlier
 *     blocks have already faded;
 *   - a block over MAX_BLOCK_CHARS is left plain;
 *   - code is skipped, and so are emoji: `compactEmoji` in ChatMarkdown needs to
 *     see them as plain strings;
 *   - any failure leaves the tree untouched, so the reply still renders.
 */
const MAX_BLOCK_CHARS = 4000;
const SKIP = new Set(["pre", "code"]);
const SEGMENTER = new Intl.Segmenter(undefined, { granularity: "grapheme" });

export const EMOJI_GRAPHEME = /\p{Extended_Pictographic}|\p{Regional_Indicator}|[#*0-9]️?⃣/u;

type Parent = { children: ElementContent[] };

function charSpan(char: string): Element {
	return {
		type: "element",
		tagName: "span",
		properties: { className: ["stream-char"] },
		children: [{ type: "text", value: char }],
	};
}

function textLength(parent: Parent): number {
	let length = 0;
	for (const child of parent.children) {
		if (child.type === "text") length += child.value.length;
		else if (child.type === "element") length += textLength(child);
	}
	return length;
}

// Characters of `value` to keep plain: the source before `settledOffset`,
// rounded down to a grapheme boundary so an emoji is never split.
function settledPrefix(value: string, start: number, settledOffset: number): number {
	const limit = Math.min(value.length, Math.max(0, settledOffset - start));
	if (limit === 0 || limit === value.length) return limit;
	let boundary = 0;
	for (const { index } of SEGMENTER.segment(value)) {
		if (index > limit) break;
		boundary = index;
	}
	return boundary;
}

function wrap(parent: Parent & { position?: Element["position"] }, settledOffset: number) {
	parent.children = parent.children.flatMap((child): ElementContent[] => {
		// Whitespace-only text is structural (between table rows, list items).
		if (child.type === "text") {
			if (child.value.trim() === "") return [child];
			// Source text that was already on screen stays plain, so it never fades in
			// again. Text a remark plugin split off has no position: judge it by its
			// enclosing element instead.
			const start = child.position?.start.offset;
			const parentEnd = parent.position?.end.offset;
			if (start === undefined && parentEnd !== undefined && parentEnd <= settledOffset) return [child];
			const kept = start === undefined ? 0 : settledPrefix(child.value, start, settledOffset);
			if (kept === child.value.length) return [child];
			const plain: ElementContent[] = kept > 0 ? [{ type: "text", value: child.value.slice(0, kept) }] : [];
			return plain.concat(Array.from(SEGMENTER.segment(child.value.slice(kept)), ({ segment }): ElementContent =>
				EMOJI_GRAPHEME.test(segment) ? { type: "text", value: segment } : charSpan(segment),
			));
		}
		if (child.type === "element" && !SKIP.has(child.tagName)) wrap(child, settledOffset);
		return [child];
	});
}

export type StreamFadeOptions = {
	/**
	 * Markdown source length already shown before streaming resumed (a chat opened
	 * mid-reply). Characters before it render plain; only later ones fade in.
	 */
	settledOffset?: number;
};

export function rehypeStreamFade({ settledOffset = 0 }: StreamFadeOptions = {}) {
	return (tree: Root) => {
		try {
			const tail = tree.children.findLast((child) => child.type === "element") as Element | undefined;
			if (tail && !SKIP.has(tail.tagName) && textLength(tail) <= MAX_BLOCK_CHARS) wrap(tail, settledOffset);
		} catch {
			// Leave the reply readable rather than fail the render.
		}
	};
}
