import { unified } from "unified";
import remarkParse from "remark-parse";
import remarkGfm from "remark-gfm";
import type { Root, RootContent } from "mdast";
import foldExceptions from "./chat-find-fold.json";

export type FindOccurrence = { messageId: string; revision: number; sequence: number; block: number; start: number; end: number };

const exceptions: Record<string, string> = foldExceptions;
const findWhitespace = /[\p{White_Space}\uFEFF]/u;
export const isEmptyFindQuery = (query: string): boolean => /^[\p{White_Space}\uFEFF]*$/u.test(query);
const inlineKinds = new Set(["text", "inlineCode", "html", "emphasis", "strong", "delete", "link", "linkReference", "break"]);

/** The daemon uses Goldmark; shared fixtures lock the displayed-text boundary. */
export function findBlocks(source: string, isMarkdown: boolean): string[] {
	if (!isMarkdown) return [source];
	const markdown = unified().use(remarkParse).use(remarkGfm);
	const blocks: string[] = [];
	const inline = (node: RootContent): string => {
		if (node.type === "image" || node.type === "imageReference") return "";
		if (node.type === "break") return "\n";
		if ("value" in node) return node.value;
		return "children" in node ? node.children.map((child) => inline(child as RootContent)).join("") : "";
	};
	const visit = (node: Root | RootContent) => {
		if (node.type === "code") {
			if (!["mermaid", "math"].includes((node.lang ?? "").toLowerCase())) blocks.push(`${node.value}\n`);
		} else if ("children" in node) {
			if (node.children.every((child) => inlineKinds.has(child.type) || child.type === "image" || child.type === "imageReference")) {
				const value = node.children.map((child) => inline(child as RootContent)).join("");
				if (value) blocks.push(value);
			} else node.children.forEach((child) => visit(child as RootContent));
		} else if (node.type === "html") blocks.push(node.value);
	};
	visit(markdown.parse(source) as Root);
	return blocks;
}

export function foldFindText(source: string): { text: string; starts: number[]; ends: number[]; identity?: boolean } {
	if (/^[\x00-\x7F]*$/.test(source)) {
		const folded = source.toLowerCase().replace(/[\t-\r]/g, " ");
		if (!/ {2}/.test(folded)) return { text: folded, starts: [], ends: [], identity: true };
		let text = "";
		const starts: number[] = [], ends: number[] = [];
		for (let i = 0; i < folded.length; i++) {
			if (folded[i] === " " && text.endsWith(" ")) { ends[ends.length - 1] = i+1; continue; }
			text += folded[i]; starts.push(i); ends.push(i+1);
		}
		return { text, starts, ends };
	}
	let text = "";
	const starts: number[] = [], ends: number[] = [];
	const segments = new Intl.Segmenter("und", { granularity: "grapheme" }).segment(source);
	for (const { segment, index } of segments) {
		const folded = Array.from(segment.normalize("NFD"), (character) => exceptions[character] ?? character.toLowerCase()).join("");
		for (const character of folded) {
			const value = findWhitespace.test(character) ? " " : character;
			if (value === " " && text.endsWith(" ")) { ends[ends.length - 1] = index + segment.length; continue; }
			text += value;
			for (let i = 0; i < value.length; i++) { starts.push(index); ends.push(index + segment.length); }
		}
	}
	return { text, starts, ends };
}

export function findTextOccurrences(source: string, query: string): Array<{ start: number; end: number }> {
	return [...findTextRanges(source, query)];
}

export function* findTextRanges(source: string, query: string): Generator<{ start: number; end: number }> {
	const needle = foldFindText(query).text.trim();
	if (!needle) return;
	const folded = foldFindText(source);
	for (let from = 0; from < folded.text.length;) {
		const start = folded.text.indexOf(needle, from);
		if (start < 0) break;
		const end = start + needle.length;
		if (/^\p{M}/u.test(folded.text.slice(end))) { from = end; continue; }
		yield { start: folded.identity ? start : folded.starts[start], end: folded.identity ? end : folded.ends[end - 1] };
		from = end;
		while (!folded.identity && from < folded.text.length && folded.starts[from] < folded.ends[end - 1]) from++;
	}
}

export function occurrenceKey(hit: FindOccurrence): string {
	return `${hit.messageId}:${hit.block}:${hit.start}:${hit.end}`;
}

export function renderedFindBlocks(root: HTMLElement): Array<{ text: string; nodes: Text[] }> {
	const blocks = new Map<Element, { text: string; nodes: Text[] }>();
	// Inline file references are message text rendered as buttons, not toolbar controls.
	const excluded = "button:not([data-chat-find-include]), svg, img, iframe, script, style, [aria-hidden='true'], .katex, [data-chat-find-exclude]";
	const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT | NodeFilter.SHOW_ELEMENT);
	while (walker.nextNode()) {
		const current = walker.currentNode;
		if (current.nodeType === Node.ELEMENT_NODE && (current as Element).tagName !== "BR") continue;
		// React Markdown emits <br> followed by a newline text node for a hard
		// break. Use that real node once, preserving offsets and Range endpoints.
		if (current.nodeType === Node.ELEMENT_NODE && current.nextSibling?.nodeType === Node.TEXT_NODE && current.nextSibling.textContent?.startsWith("\n")) continue;
		const node = current.nodeType === Node.TEXT_NODE ? current as Text : document.createTextNode("\n");
		const parent = current.parentElement;
		if (!node.data || parent?.closest(excluded)) continue;
		const closest = parent?.closest("pre, p, h1, h2, h3, h4, h5, h6, td, th, li");
		const element = closest && root.contains(closest) ? closest : root;
		// Ignore layout whitespace between independent Markdown blocks. A loose list
		// item holds "\n" beside its <p>; that must not count as a block of its own.
		if ((element === root || element.tagName === "LI" && !blocks.has(element)) && !node.data.trim()) continue;
		const block = blocks.get(element) ?? { text: "", nodes: [] };
		block.text += node.data; block.nodes.push(node); blocks.set(element, block);
	}
	return [...blocks.values()];
}

export function findRange(nodes: Text[], start: number, end: number): Range | undefined {
	const range = document.createRange();
	let offset = 0, started = false;
	for (const node of nodes) {
		const next = offset + node.length;
		if (!started && start < next) { range.setStart(node, start - offset); started = true; }
		if (started && end <= next) { range.setEnd(node, end - offset); return range; }
		offset = next;
	}
	return undefined;
}
