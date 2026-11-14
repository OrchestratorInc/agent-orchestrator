import type { ChatExcerptSnapshot } from "../../shared/chat-excerpt";
import { renderedExcerptRange } from "./chat-excerpt-snapshot";
/** Find a rendered selection across Markdown spans without changing DOM or selection. */
export function annotationTextRange(root: HTMLElement, text: string): Range | undefined {
	const nodes: Text[] = [];
	const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
	while (walker.nextNode()) nodes.push(walker.currentNode as Text);
	const raw = nodes.map((node) => node.data).join("");
	// Browser selections collapse whitespace differently around rendered Markdown.
	const positions: number[] = [];
	let normalized = "";
	for (let i = 0; i < raw.length; i++) {
		const character = /\s/.test(raw[i]) ? " " : raw[i];
		if (character === " " && normalized.endsWith(" ")) continue;
		normalized += character;
		positions.push(i);
	}
	const selection = text.replace(/\s+/g, " ").trim();
	const index = normalized.indexOf(selection);
	if (!selection || index < 0) return undefined;
	const start = positions[index];
	const end = positions[index + selection.length - 1] + 1;
	const range = document.createRange();
	let offset = 0;
	let started = false;
	for (const node of nodes) {
		const next = offset + node.length;
		if (!started && start < next) {
			range.setStart(node, start - offset);
			started = true;
		}
		if (started && end <= next) {
			range.setEnd(node, end - offset);
			return range;
		}
		offset = next;
	}
	return undefined;
}

/** Sent from navigated text so whatever folds it (a long user message) can open. */
export const CHAT_REVEAL_RANGE_EVENT = "chat-reveal-range";
export type ChatRevealRangeDetail = { range: Range };

/**
 * Asks whatever folds the range to open. A long user message folds behind Show
 * more; navigation that centers a match below the fold would land on clipped
 * text. Returns true when something opened, so the caller measures once the new
 * layout has rendered instead of centering text the fold still hides.
 */
export function revealFoldedRange(range: Range): boolean {
	const node = range.startContainer;
	const element = node instanceof Element ? node : node.parentElement;
	if (!element) return false;
	return !element.dispatchEvent(new CustomEvent<ChatRevealRangeDetail>(CHAT_REVEAL_RANGE_EVENT, {
		bubbles: true,
		cancelable: true,
		detail: { range },
	}));
}

export function annotationBody(source: HTMLElement): HTMLElement {
	return source.querySelector<HTMLElement>("[data-chat-message-body]") ?? source;
}

/** Match a source even when Markdown rendering cannot produce an exact Range. */
export function annotationTextMatches(root: HTMLElement, text: string): boolean {
	if (annotationTextRange(root, text)) return true;
	const selected = text.replace(/\s+/g, " ").trim();
	const rendered = (root.textContent ?? "").replace(/\s+/g, " ").trim();
	return Boolean(selected && rendered.includes(selected));
}

/** Resolve a captured selection only against its unchanged source revision. */
export function annotationReferenceRange(source: HTMLElement, reference: { text: string; revision?: number; snapshot?: ChatExcerptSnapshot }): Range | undefined {
	const currentRevision = Number(source.dataset.chatMessageRevision);
	if (reference.revision !== undefined && reference.revision !== currentRevision) return undefined;
	if (reference.snapshot) {
		if (reference.revision === undefined) return undefined;
		return renderedExcerptRange(annotationBody(source), reference.text, reference.snapshot);
	}
	return annotationTextRange(annotationBody(source), reference.text);
}

/** Resolve a diagram by its original block positions, never by its label text. */
export function annotationDiagramBlock(source: HTMLElement, reference: { revision?: number; snapshot?: ChatExcerptSnapshot }): HTMLElement | undefined {
	const snapshot = reference.snapshot;
	if (!snapshot?.containsFlowchart || reference.revision === undefined || reference.revision !== Number(source.dataset.chatMessageRevision)) return undefined;
	// Older chart-only captures already have the exact block as their raw range.
	const range = snapshot.diagramRange ?? snapshot.sourceRange;
	if (!range || range.offsetUnit !== "utf-16") return undefined;
	const blocks = Array.from(annotationBody(source).querySelectorAll<HTMLElement>("[data-excerpt-flowchart]"))
		.filter(block => Number(block.dataset.excerptStart) === range.start && Number(block.dataset.excerptEnd) === range.end);
	return blocks.length === 1 ? blocks[0] : undefined;
}

/** Returns cleanup so a later jump/unmount cannot clear another highlight. */
export function highlightChatAnnotation(source: HTMLElement, text: string, revision?: number, snapshot?: ChatExcerptSnapshot): () => void {
	const body = annotationBody(source);
	const range = annotationReferenceRange(source, { text, revision, snapshot });
	const highlightApi = globalThis as unknown as {
		Highlight?: new (...ranges: Range[]) => unknown;
		CSS?: { highlights?: Map<string, unknown> };
	};
	const registry = highlightApi.CSS?.highlights;
	const highlight = range && highlightApi.Highlight ? new highlightApi.Highlight(range) : undefined;
	if (highlight && registry) registry.set("chat-annotation", highlight);
	else if (!snapshot) body.classList.add("chat-annotation-target");
	return () => {
		if (registry?.get("chat-annotation") === highlight) registry?.delete("chat-annotation");
		body.classList.remove("chat-annotation-target");
	};
}

/**
 * A search snippet is one raw Markdown line; the transcript shows it rendered.
 * Drop the inline markup so the line can be found in the rendered message.
 */
export function renderedSnippetText(line: string): string {
	return line
		.replace(/!?\[([^\]]*)\]\([^)]*\)/g, "$1")
		.replace(/^\s*(?:#{1,6}\s+|(?:>\s*)+|[-*+]\s+(?:\[[ xX]\]\s+)?|\d+[.)]\s+)/, "")
		.replace(/\*\*|__|~~|`|\*/g, "")
		.replace(/\s+/g, " ")
		.trim();
}
