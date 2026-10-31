import {
	MAX_EXCERPT_BYTES,
	MAX_EXCERPT_SEGMENTS,
	type ChatExcerptSegment,
} from "../../shared/chat-excerpt";
import type { ChatDraftExcerptReference } from "./chat-drafts";

const CONTENT = "[data-chat-message-body], [data-chat-command-field]";
export const CHAT_SELECTION_IGNORE =
	'[data-chat-selection-ignore], button:not([data-chat-selection-content]), [aria-hidden="true"], .sr-only, script, style';
const IGNORE = CHAT_SELECTION_IGNORE;

function clippedRange(range: Range, element: Element): Range | undefined {
	if (!range.intersectsNode(element)) return;
	const clipped = range.cloneRange();
	if (!element.contains(range.startContainer)) clipped.setStart(element, 0);
	if (!element.contains(range.endContainer))
		clipped.setEnd(element, element.childNodes.length);
	// cloneContents omits ancestors above a boundary text node. Clip against the
	// original DOM first, otherwise a selection inside "Copy" loses its marker.
	const ignoredParent = (node: Node) => {
		let ignored: Element | undefined;
		for (
			let parent = node instanceof Element ? node : node.parentElement;
			parent && element.contains(parent);
			parent = parent.parentElement
		) {
			if (parent.matches(IGNORE)) ignored = parent;
			if (parent === element) break;
		}
		return ignored;
	};
	const start = ignoredParent(clipped.startContainer),
		end = ignoredParent(clipped.endContainer);
	if (start && start === end) return;
	if (start) clipped.setStartAfter(start);
	if (end) clipped.setEndBefore(end);
	return clipped.collapsed ? undefined : clipped;
}

/** Preserve preformatted text and structural breaks, without reading UI labels. */
function contentText(fragment: DocumentFragment): string {
	fragment.querySelectorAll(IGNORE).forEach((element) => element.remove());
	const block = /^(P|DIV|PRE|LI|TR|H[1-6]|BLOCKQUOTE)$/;
	const pieces: { text: string; structural: boolean }[] = [];
	const write = (text: string, structural = false) => {
		if (text) pieces.push({ text, structural });
	};
	const read = (node: Node): void => {
		if (node.nodeType === Node.TEXT_NODE) {
			write(node.textContent ?? "");
			return;
		}
		if (!(node instanceof Element) && !(node instanceof DocumentFragment))
			return;
		if (node instanceof Element && node.tagName === "BR") {
			write("\n");
			return;
		}
		const isBlock = node instanceof Element && block.test(node.tagName);
		if (isBlock) write("\n", true);
		if (node instanceof Element && node.tagName === "PRE")
			write(node.textContent ?? "");
		else node.childNodes.forEach(read);
		if (
			node instanceof Element &&
			(node.tagName === "TD" || node.tagName === "TH")
		)
			write("\t", true);
		if (isBlock) write("\n", true);
	};
	read(fragment);
	while (pieces[0]?.structural) pieces.shift();
	while (pieces.at(-1)?.structural) pieces.pop();
	return pieces.map((piece) => piece.text).join("");
}

function captureExcerpt(
	range: Range,
	root: HTMLElement,
	conversationId: string,
	completedTurns: ReadonlySet<string>,
): ChatDraftExcerptReference | undefined {
	if (
		range.collapsed ||
		!range.startContainer.isConnected ||
		!range.endContainer.isConnected ||
		!root.contains(range.startContainer) ||
		!root.contains(range.endContainer)
	)
		return;
	for (const element of root.querySelectorAll<HTMLElement>(
		"[data-chat-selection-unsupported]",
	)) {
		const selected = clippedRange(range, element);
		if (
			selected &&
			(element.dataset.chatSelectionUnsupported === "diagram" ||
				contentText(selected.cloneContents()))
		)
			throw new ExcerptSelectionError(
				element.dataset.chatSelectionUnsupported === "diagram"
					? "diagram"
					: element.dataset.chatSelectionUnsupported === "formatted"
						? "formatted"
						: "unsupported",
			);
	}
	const parts: ChatExcerptSegment[] = [];
	const turns = new Set<string>();
	let missingTurn = false;
	let role: "user" | "assistant" = "assistant";
	for (const element of root.querySelectorAll<HTMLElement>(CONTENT)) {
		const selected = clippedRange(range, element);
		if (!selected) continue;
		const command = element.hasAttribute("data-chat-command-field");
		const fragment = selected.cloneContents();
		const text = contentText(fragment);
		if (!text.trim()) continue;
		const source = element.closest<HTMLElement>(
			command ? "[data-chat-activity-id]" : "[data-chat-message-id]",
		);
		if (!source) throw new ExcerptSelectionError("changing");
		const revision = Number(
			command
				? source.dataset.chatActivityRevision
				: source.dataset.chatMessageRevision,
		);
		const sourceId = command
			? source.dataset.chatActivityId
			: source.dataset.chatMessageId;
		if (!sourceId || !Number.isSafeInteger(revision) || revision < 0)
			throw new ExcerptSelectionError("stale");
		const turnId = source.dataset.chatExcerptTurnId;
		if (turnId) turns.add(turnId);
		else {
			missingTurn = true;
			if (command) throw new ExcerptSelectionError("turn");
		}
		if (command && source.dataset.chatActivitySettled !== "true")
			throw new ExcerptSelectionError("changing");
		role =
			source.dataset.chatMessageRole === "user" && parts.length === 0
				? "user"
				: "assistant";
		parts.push({
			kind: command ? "command" : "message",
			sourceId,
			revision,
			text,
			...(command
				? {
						field: element.dataset
							.chatCommandField as ChatExcerptSegment["field"],
					}
				: {}),
		});
		if (parts.length > MAX_EXCERPT_SEGMENTS)
			throw new ExcerptSelectionError("segments");
	}
	if (!parts.length) return;
	const text = parts.map((part) => part.text).join("\n\n");
	if (new TextEncoder().encode(text).length > MAX_EXCERPT_BYTES)
		throw new ExcerptSelectionError("bytes");
	const legacy =
		parts.length === 1 &&
		parts[0].kind === "message" &&
		parts[0].text === parts[0].text.trim();
	if (
		!legacy &&
		(missingTurn || turns.size !== 1 || !completedTurns.has([...turns][0]))
	)
		throw new ExcerptSelectionError("turn");
	return {
		id: crypto.randomUUID(),
		conversationId,
		text,
		role,
		messageId: legacy ? parts[0].sourceId : "",
		revision: legacy ? parts[0].revision : 0,
		...(legacy ? {} : { segments: parts }),
	};
}

export type ChatExcerptReason =
	| "unsupported"
	| "diagram"
	| "formatted"
	| "changing"
	| "stale"
	| "turn"
	| "segments"
	| "bytes";
class ExcerptSelectionError extends Error {
	constructor(readonly reason: ChatExcerptReason) {
		super(reason);
	}
}
export type ChatExcerptSelection =
	| { kind: "supported"; excerpt: ChatDraftExcerptReference }
	| { kind: "unsupported"; reason: ChatExcerptReason };
export function captureChatExcerptSelection(
	...args: Parameters<typeof captureExcerpt>
): ChatExcerptSelection | undefined {
	try {
		const excerpt = captureExcerpt(...args);
		return excerpt ? { kind: "supported", excerpt } : undefined;
	} catch (error) {
		if (error instanceof ExcerptSelectionError)
			return { kind: "unsupported", reason: error.reason };
		throw error;
	}
}
/** Content-only extraction used by provenance tests and source navigation. */
export const captureChatExcerpt = captureExcerpt;
