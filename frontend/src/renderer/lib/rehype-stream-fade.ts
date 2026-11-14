import type { Element, ElementContent, Root } from "hast";

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
		if (child.type === "text") {
			if (child.value.trim() === "") return [child];
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
	settledOffset?: number;
};

export function rehypeStreamFade({ settledOffset = 0 }: StreamFadeOptions = {}) {
	return (tree: Root) => {
		try {
			const tail = tree.children.findLast((child) => child.type === "element") as Element | undefined;
			if (tail && !SKIP.has(tail.tagName) && textLength(tail) <= MAX_BLOCK_CHARS) wrap(tail, settledOffset);
		} catch {
		}
	};
}
