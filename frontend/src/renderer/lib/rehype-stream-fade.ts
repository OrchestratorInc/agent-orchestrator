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

function wrap(parent: Parent) {
	parent.children = parent.children.flatMap((child): ElementContent[] => {
		// Whitespace-only text is structural (between table rows, list items).
		if (child.type === "text") {
			if (child.value.trim() === "") return [child];
			return Array.from(SEGMENTER.segment(child.value), ({ segment }): ElementContent =>
				EMOJI_GRAPHEME.test(segment) ? { type: "text", value: segment } : charSpan(segment),
			);
		}
		if (child.type === "element" && !SKIP.has(child.tagName)) wrap(child);
		return [child];
	});
}

export function rehypeStreamFade() {
	return (tree: Root) => {
		try {
			const tail = tree.children.findLast((child) => child.type === "element") as Element | undefined;
			if (tail && !SKIP.has(tail.tagName) && textLength(tail) <= MAX_BLOCK_CHARS) wrap(tail);
		} catch {
			// Leave the reply readable rather than fail the render.
		}
	};
}
