import type { Element, ElementContent, Root } from "hast";

/**
 * Replaces each text node of the last block with `<span class="stream-text">`,
 * which ChatMarkdown renders as the text with every character from `dataKept`
 * on in its own `<span class="stream-char">`, keyed by source offset, so CSS
 * fades each one in once as it mounts. Every text node of the block gets the
 * wrapper, settled or not, so a wrapper's position among its siblings, which
 * react-markdown keys it by, does not move as characters settle. Apply only
 * while streaming; a settled reply drops the plugin and renders plain markdown.
 *
 * Bounds, so a long reply cannot make every update expensive or unsafe:
 *   - only the last block is wrapped: text streams in at the tail, and earlier
 *     blocks have already faded;
 *   - a block over MAX_BLOCK_CHARS is left plain;
 *   - code is skipped;
 *   - any failure leaves the tree untouched, so the reply still renders.
 */
const MAX_BLOCK_CHARS = 1000;
const SKIP = new Set(["pre", "code"]);
const SEGMENTER = new Intl.Segmenter(undefined, { granularity: "grapheme" });

export const EMOJI_GRAPHEME = /\p{Extended_Pictographic}|\p{Regional_Indicator}|[#*0-9]️?⃣/u;

type Parent = { children: ElementContent[] };

function textSpan(value: string, start: number | undefined, kept: number): Element {
	return {
		type: "element",
		tagName: "span",
		properties: { className: ["stream-text"], dataStart: start, dataKept: kept },
		children: [{ type: "text", value }],
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
	parent.children = parent.children.map((child): ElementContent => {
		// Whitespace-only text is structural (between table rows, list items).
		if (child.type === "text") {
			if (child.value.trim() === "") return child;
			// Source text that was already on screen stays plain, so it never fades in
			// again. Text a remark plugin split off has no position: judge it by its
			// enclosing element instead.
			const start = child.position?.start.offset;
			const end = child.position?.end.offset;
			const parentEnd = parent.position?.end.offset;
			let kept: number;
			if (start === undefined) kept = parentEnd !== undefined && parentEnd <= settledOffset ? child.value.length : 0;
			// A character reference or escape makes the source longer than the text,
			// so source offsets do not index it: the node settles as a whole.
			else if (end !== undefined && end - start !== child.value.length) kept = end <= settledOffset ? child.value.length : 0;
			else kept = settledPrefix(child.value, start, settledOffset);
			return textSpan(child.value, start, kept);
		}
		if (child.type === "element" && !SKIP.has(child.tagName)) wrap(child, settledOffset);
		return child;
	});
}

export type StreamFadeOptions = {
	/**
	 * Markdown source length that is not fading in: shown before streaming
	 * resumed (a chat opened mid-reply), or arrived longer ago than the fade
	 * lasts. Characters before it render plain; only later ones fade in.
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
