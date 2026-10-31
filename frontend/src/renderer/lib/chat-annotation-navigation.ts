import type { ChatExcerptSegment } from "../../shared/chat-excerpt";
import { CHAT_SELECTION_IGNORE } from "./chat-excerpt-selection";

/** Find a rendered selection across Markdown spans without changing DOM or selection. */
export function annotationTextRange(root: HTMLElement, text: string): Range | undefined {
	const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
	const positions: { node: Text; offset: number }[] = [];
	let normalized = "";
	let previousBlock: Element | null | undefined;
	while (walker.nextNode()) {
		const node = walker.currentNode as Text;
		if (node.parentElement?.closest(CHAT_SELECTION_IGNORE)) continue;
		const block = node.parentElement?.closest("p, pre, li, tr, td, th, h1, h2, h3, h4, h5, h6, blockquote");
		if (normalized && block !== previousBlock && !normalized.endsWith(" ")) {
			normalized += " "; positions.push({ node, offset: 0 });
		}
		previousBlock = block;
		for (let i = 0; i < node.data.length; i++) {
			const character = /\s/.test(node.data[i]) ? " " : node.data[i];
			if (character === " " && normalized.endsWith(" ")) continue;
			normalized += character; positions.push({ node, offset: i });
		}
	}
	const selection = text.replace(/\s+/g, " ").trim();
	const index = normalized.indexOf(selection);
	if (!selection || index < 0) return undefined;
	const start = positions[index];
	const end = positions[index + selection.length - 1];
	const range = document.createRange();
	range.setStart(start.node, start.offset); range.setEnd(end.node, end.offset + 1);
	return range;
}

export function excerptSegmentElement(root: HTMLElement, part: ChatExcerptSegment): HTMLElement | undefined {
	const source = [...root.querySelectorAll<HTMLElement>(part.kind === "message" ? "[data-chat-message-id]" : "[data-chat-activity-id]")]
		.find(element => (part.kind === "message" ? element.dataset.chatMessageId : element.dataset.chatActivityId) === part.sourceId);
	if (part.kind === "message") return source ? annotationBody(source) : undefined;
	return source ? [...source.querySelectorAll<HTMLElement>("[data-chat-command-field]")].find(element => element.dataset.chatCommandField === part.field) : undefined;
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

/** Returns cleanup so a later jump/unmount cannot clear another highlight. */
export function highlightChatAnnotation(source: HTMLElement, text: string, revision?: number, segments?: ChatExcerptSegment[], scope?: HTMLElement): () => void {
	const body = annotationBody(source);
	const currentRevision = Number(source.dataset.chatMessageRevision ?? source.dataset.chatActivityRevision);
	const range = revision === undefined || revision === currentRevision ? annotationTextRange(body, text) : undefined;
	const targets = segments ? segments.map(part => {
		const element = excerptSegmentElement(scope ?? source.closest<HTMLElement>("[data-chat-turn-source]") ?? source.parentElement!, part);
		const owner = element?.closest<HTMLElement>("[data-chat-message-id], [data-chat-activity-id]");
		return element && Number(owner?.dataset.chatMessageRevision ?? owner?.dataset.chatActivityRevision) === part.revision ? { element, range: annotationTextRange(element, part.text) } : undefined;
	}).filter((entry): entry is {element: HTMLElement; range: Range | undefined} => Boolean(entry)) : [{ element: body, range }];
	const ranges = targets.flatMap(target => target.range ? [target.range] : []);
	const highlightApi = globalThis as unknown as {
		Highlight?: new (...ranges: Range[]) => unknown;
		CSS?: { highlights?: Map<string, unknown> };
	};
	const registry = highlightApi.CSS?.highlights;
	const highlight = ranges.length && highlightApi.Highlight ? new highlightApi.Highlight(...ranges) : undefined;
	if (highlight && registry) registry.set("chat-annotation", highlight);
	else targets.forEach(target => target.element.classList.add("chat-annotation-target"));
	return () => {
		if (registry?.get("chat-annotation") === highlight) registry?.delete("chat-annotation");
		targets.forEach(target => target.element.classList.remove("chat-annotation-target"));
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
