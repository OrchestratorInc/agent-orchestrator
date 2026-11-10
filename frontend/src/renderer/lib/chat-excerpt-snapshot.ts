import { EXCERPT_TURN_BYTES, truncateExcerptUtf8, utf8Bytes, type ChatExcerptSnapshot } from "../../shared/chat-excerpt";
import type { ConversationMessage, ConversationSnapshot } from "../types/conversation";
import type { ChatDraftExcerptReference } from "./chat-drafts";
import { codeSourceOffsets, excerptSourceOffsets } from "./rehype-excerpt-source";

const BLOCKS = new Set(["P", "PRE", "LI", "TR", "H1", "H2", "H3", "H4", "H5", "H6", "BLOCKQUOTE"]);
const EXCLUDED = 'button:not([data-excerpt-include]), input, select, textarea, [hidden], .katex-mathml, [data-excerpt-exclude]';
type TextEntry = { node: Text; start: number; end: number };

/** One projection for capture and navigation, independent of browser whitespace rules. */
export function excerptRenderedText(root: HTMLElement): { text: string; entries: TextEntry[] } {
	let text = "";
	const entries: TextEntry[] = [];
	const markdown = root.querySelector(".chat-md");
	const newline = () => { if (text && !text.endsWith("\n")) text += "\n"; };
	function walk(node: Node) {
		if (node instanceof Text) {
			// Newlines between Markdown blocks are structural, not selectable content.
			if (!node.data.trim() && node.parentElement === markdown) return;
			// remark-rehype emits both a BR and a formatting newline for hard breaks.
			if (node.data === "\n" && node.previousSibling instanceof Element && node.previousSibling.tagName === "BR") return;
			entries.push({ node, start: text.length, end: text.length + node.length });
			text += node.data;
			return;
		}
		if (!(node instanceof Element) || node.matches(EXCLUDED)) return;
		if (node.getAttribute("aria-hidden") === "true" && !node.closest("[data-math]")) return;
		if (node.tagName === "BR") { text += "\n"; return; }
		if (BLOCKS.has(node.tagName)) newline();
		if ((node.tagName === "TD" || node.tagName === "TH") && node.previousElementSibling) text += "\t";
		for (const child of node.childNodes) walk(child);
		if (BLOCKS.has(node.tagName)) newline();
	}
	walk(root);
	return { text, entries };
}

export function captureExcerptSnapshot(body: HTMLElement, range: Range, source: string): { text: string; snapshot: ChatExcerptSnapshot } {
	const rendered = excerptRenderedText(body);
	const selected = rendered.entries.flatMap(entry => {
		if (!range.intersectsNode(entry.node) || range.comparePoint(entry.node, entry.node.length) < 0 || range.comparePoint(entry.node, 0) > 0) return [];
		const start = range.startContainer === entry.node ? range.startOffset : 0;
		const end = range.endContainer === entry.node ? range.endOffset : entry.node.length;
		return end > start ? [{ ...entry, from: start, to: end }] : [];
	});
	if (!selected.length) throw new Error("chat.draft.excerptBodySelection");
	// Only selected source elements need detailed mappings. Keep them local to
	// this capture so rerenders and subsequent selections cannot reuse stale DOM.
	const maps = new Map<HTMLElement, number[] | undefined>();
	const broadBlocks = new Set(body.querySelectorAll<HTMLElement>("[data-excerpt-block]"));
	for (const entry of selected) {
		const element = entry.node.parentElement?.closest<HTMLElement>("[data-excerpt-kind]");
		if (!element || maps.has(element)) continue;
		const start = Number(element.dataset.excerptStart), end = Number(element.dataset.excerptEnd);
		let map: number[] | undefined;
		if (Number.isSafeInteger(start) && Number.isSafeInteger(end) && start >= 0 && end > start && end <= source.length) {
			const raw = source.slice(start, end), visible = element.textContent ?? "";
			map = element.dataset.excerptKind === "text" ? excerptSourceOffsets(raw, visible, start)
				: codeSourceOffsets(raw, visible, start, element.dataset.excerptKind === "block-code");
		}
		maps.set(element, map);
		if (!map) broadBlocks.add(element);
	}
	function rawBoundary(node: Text, offset: number, endBoundary = false): { position: number; broad: boolean } | undefined {
		const element = node.parentElement?.closest<HTMLElement>("[data-excerpt-kind]");
		const map = element && maps.get(element);
		if (element && map) {
			const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT);
			let local = offset;
			while (walker.nextNode() && walker.currentNode !== node) local += (walker.currentNode as Text).length;
			const position = map[local];
			if (Number.isSafeInteger(position)) return { position, broad: false };
		}
		const block = node.parentElement?.closest<HTMLElement>("[data-excerpt-start][data-excerpt-end]");
		if (!block) return undefined;
		return { position: Number(endBoundary ? block.dataset.excerptEnd : block.dataset.excerptStart), broad: true };
	}
	const first = selected[0];
	const last = selected[selected.length - 1];
	const start = first.start + first.from;
	const end = last.start + last.to;
	let text = rendered.text.slice(start, end);
	if (!text.trim()) throw new Error("chat.draft.excerptBodySelection");
	const chartEntries = selected.filter(entry => entry.node.parentElement?.closest("[data-excerpt-flowchart]"));
	// Keep the original projection for source coverage; diagram labels and source
	// belong only to the raw excerpt, never to the readable quote.
	for (const entry of [...chartEntries].reverse()) {
		const from = entry.start + entry.from - start;
		const to = entry.start + entry.to - start;
		text = text.slice(0, from) + text.slice(to);
	}
	if (chartEntries.length) text = text.trim();
	const containsFlowchart = chartEntries.length > 0;
	const badgeOnly = containsFlowchart && !text.trim();
	let rawStart = rawBoundary(first.node, first.from);
	let rawEnd = rawBoundary(last.node, last.to, true);
	// Plain user messages are not Markdown-rendered and can map directly.
	if (!rawStart && !rawEnd && rendered.text.replace(/\n$/, "") === source) {
		rawStart = { position: start, broad: false };
		rawEnd = { position: end, broad: false };
	}
	let from = rawStart?.position ?? 0;
	let to = rawEnd?.position ?? source.length;
	let broad = !rawStart || !rawEnd || rawStart.broad || rawEnd.broad;
	// A transformed block touched inside the range needs its full underlying source.
	for (const block of broadBlocks) {
		if (!selected.some(entry => block.contains(entry.node))) continue;
		from = Math.min(from, Number(block.dataset.excerptStart));
		to = Math.max(to, Number(block.dataset.excerptEnd));
		broad = true;
	}
	if (!Number.isSafeInteger(from) || !Number.isSafeInteger(to) || from < 0 || to <= from || to > source.length) {
		from = 0; to = source.length; broad = true;
	}
	const diagram = chartEntries[0]?.node.parentElement?.closest<HTMLElement>("[data-excerpt-flowchart]");
	const diagramStart = Number(diagram?.dataset.excerptStart);
	const diagramEnd = Number(diagram?.dataset.excerptEnd);
	const diagramRange = Number.isSafeInteger(diagramStart) && Number.isSafeInteger(diagramEnd) && diagramStart >= from && diagramEnd > diagramStart && diagramEnd <= to
		? { start: diagramStart, end: diagramEnd, offsetUnit: "utf-16" as const } : undefined;
	return { text, snapshot: {
		version: 1, referenceFormat: "numbered-v1", rawText: source.slice(from, to), captureMode: broad ? "block" : "exact",
		...(containsFlowchart ? { containsFlowchart: true, ...(badgeOnly ? { badgeOnly: true } : {}) } : {}),
		sourceRange: { start: from, end: to, offsetUnit: "utf-16" },
		...(diagramRange ? { diagramRange } : {}),
		...(!containsFlowchart ? {
			renderedRange: { start, end, offsetUnit: "utf-16" as const },
			prefix: Array.from(rendered.text.slice(0, start)).slice(-64).join(""),
			suffix: Array.from(rendered.text.slice(end)).slice(0, 64).join(""),
		} : {}),
	} };
}

/** Freeze only completed-turn context already available at attachment time. */
export function captureExcerptTurn(ref: ChatDraftExcerptReference, snapshot: ConversationSnapshot): ChatDraftExcerptReference {
	if (!ref.snapshot) return ref;
	const source = snapshot.items.find((item): item is ConversationMessage => item.kind === "message" && item.id === ref.messageId);
	const next = { ...ref.snapshot, sourceTurnId: source?.turnId };
	const turn = snapshot.turns.find(turn => turn.id === source?.turnId);
	if (!source || source.revision !== ref.revision || !turn || turn.state !== "completed" || turn.rolledBack) return { ...ref, snapshot: next };
	const used = { user: 0, assistant: 0 };
	let truncated = false;
	const messages: NonNullable<ChatExcerptSnapshot["turn"]>["messages"] = [];
	for (const item of snapshot.items) {
		if (item.kind !== "message" || item.turnId !== turn.id || item.streaming || (item.role !== "user" && item.role !== "assistant")) continue;
		const available = EXCERPT_TURN_BYTES - used[item.role];
		const text = truncateExcerptUtf8(item.text, Math.max(0, available));
		used[item.role] += utf8Bytes(text);
		truncated ||= text !== item.text;
		if (text && messages.length < 128) messages.push({ role: item.role, text });
		else if (text) truncated = true;
	}
	if (messages.length) next.turn = { id: turn.id, messages, truncated };
	return { ...ref, snapshot: next };
}

export function upgradeExcerpt(ref: ChatDraftExcerptReference): ChatDraftExcerptReference {
	return { ...ref, snapshot: { ...(ref.snapshot ?? { version: 1, rawText: "", captureMode: "legacy" }), referenceFormat: "numbered-v1" } };
}

export function renderedExcerptRange(body: HTMLElement, text: string, snapshot: ChatExcerptSnapshot): Range | undefined {
	if (!text || snapshot.containsFlowchart) return undefined;
	const rendered = excerptRenderedText(body);
	const anchor = snapshot.renderedRange;
	const matches = (index: number) => rendered.text.slice(index, index + text.length) === text &&
		(!snapshot.prefix || rendered.text.slice(0, index).endsWith(snapshot.prefix)) &&
		(!snapshot.suffix || rendered.text.slice(index + text.length).startsWith(snapshot.suffix));
	let start = anchor && matches(anchor.start) && anchor.end === anchor.start + text.length ? anchor.start : -1;
	if (start < 0) {
		const candidates: number[] = [];
		for (let at = rendered.text.indexOf(text); at >= 0; at = rendered.text.indexOf(text, at + 1)) if (matches(at)) candidates.push(at);
		if (candidates.length !== 1) return undefined;
		start = candidates[0];
	}
	const first = rendered.entries.find(entry => entry.start <= start && entry.end > start);
	const last = rendered.entries.find(entry => entry.start < start + text.length && entry.end >= start + text.length);
	if (!first || !last) return undefined;
	const range = document.createRange();
	range.setStart(first.node, start - first.start);
	range.setEnd(last.node, start + text.length - last.start);
	return range;
}
