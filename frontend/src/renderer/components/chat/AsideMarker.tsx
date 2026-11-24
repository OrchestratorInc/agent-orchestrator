import { createContext, useContext, useId, useLayoutEffect, useRef, type MouseEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { MessageSquare, MessageSquarePlus } from "@/components/app-icons";
import type { CommentDraft } from "../../lib/cloud-asides";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import type { MentionTint } from "./MentionChip";

// Comments on a shared Cloud session's conversation, like comments in a
// shared document. People start a thread on any finished message (one
// Comment button on hover) or on a passage they select; nothing reaches the
// agent unless someone ticks "Also send to agent". The conversation stays
// quiet: a commented passage gets a soft highlight, and a message with open
// threads gets ONE icon in its right margin, with no count, that turns blue
// for replies the reader hasn't seen. Resolved threads draw nothing.

/** How a message's comment icon reads. */
export type CommentMarkerState = "idle" | "unread" | "active";

/** The open threads about one message, as its marker and highlights draw them. */
export interface MessageComments {
	state: CommentMarkerState;
	/** Its open threads, newest activity first. */
	asideIds: string[];
	/** The passages its threads quote, each highlighted; active while that thread is open. */
	quotes: Array<{ asideId: string; quote: string; active: boolean }>;
}

/**
 * The comment threads of the conversation on screen. The chat surface of a
 * shared Cloud session provides it once its control plane has comments;
 * without it every message draws as before.
 */
export interface CommentMarkers {
	/** People can start a comment here (the session is open). */
	canComment: boolean;
	/** The open threads about a message: by its anchor sequence, or the aside its tags started. */
	forMessage: (sequence: number | undefined, asideId?: string) => MessageComments | undefined;
	/** The message's comment icon: its only thread, or the list of its threads. */
	openMessage: (sequence: number, comments: MessageComments) => void;
	openThread: (asideId: string) => void;
	/** Comment: opens a new comment on the message (or a passage of it). */
	startComment: (draft: CommentDraft) => void;
}

export const CommentMarkersContext = createContext<CommentMarkers | undefined>(undefined);

/**
 * The column of a conversation with comments, for its messages and its
 * composer alike, so both always share the same left and right edges. A
 * message's comment icon and Comment button sit outside the column's right
 * edge (see CommentTarget), never inside it. Where the window leaves the
 * column a margin anyway, it stays the composer's usual 48rem; where it
 * doesn't, messages and composer narrow together, keeping 3rem more each
 * side: room for the icon, clear of the conversation scrollbar.
 */
export const COMMENT_COLUMN_CLASS = "max-w-[min(48rem,calc(100%_-_6rem))]";
/** The same for the composer of a conversation with nothing in it yet (its 42rem). */
export const COMMENT_EMPTY_COLUMN_CLASS = "data-[empty]:max-w-[min(42rem,calc(100%_-_6rem))]";

/** Whether a message that tags people draws as an aside's dashed box: it started one and the conversation knows its threads. */
export function useAsideMarker(asideId: string | undefined): boolean {
	const markers = useContext(CommentMarkersContext);
	return Boolean(asideId && markers);
}

/** A message with an aside looks the same for the sender and everyone it tags: no chip is tinted. */
export const NO_MENTION_TINT: MentionTint = {};

/** The dashed box a message with an aside draws as: only its text and chips, no fill. */
export function asideBoxClass(queued?: boolean): string {
	return cn(
		"w-fit max-w-full rounded-[10px] border border-dashed border-foreground/25 bg-transparent px-3 py-2.5 text-sm leading-[1.55]",
		queued ? "text-muted-foreground" : "text-foreground",
	);
}

// ---------------------------------------------------------------------------
// Highlights. Commented passages are painted with the CSS Custom Highlight
// API, so the message's rendered markdown is never rewritten: two registry
// entries, "ao-comment" and "ao-comment-active" (see styles.css).
// ---------------------------------------------------------------------------

export const COMMENT_HIGHLIGHT = "ao-comment";
export const COMMENT_HIGHLIGHT_ACTIVE = "ao-comment-active";

type HighlightApi = { Highlight?: new (...ranges: Range[]) => unknown; CSS?: { highlights?: Map<string, unknown> } };

const painted = new Map<string, { ranges: Range[]; active: Range[] }>();

function repaint(): void {
	const api = globalThis as unknown as HighlightApi;
	const registry = api.CSS?.highlights;
	if (!registry || !api.Highlight) return;
	const ranges: Range[] = [];
	const active: Range[] = [];
	for (const entry of painted.values()) {
		ranges.push(...entry.ranges);
		active.push(...entry.active);
	}
	if (ranges.length) registry.set(COMMENT_HIGHLIGHT, new api.Highlight(...ranges));
	else registry.delete(COMMENT_HIGHLIGHT);
	if (active.length) registry.set(COMMENT_HIGHLIGHT_ACTIVE, new api.Highlight(...active));
	else registry.delete(COMMENT_HIGHLIGHT_ACTIVE);
}

/**
 * The first place a passage occurs in an element's text, as a DOM range.
 * Runs of whitespace match any run of whitespace, since a selection's text
 * and the rendered text can break lines differently. Undefined when the
 * passage isn't there (the message changed, or the quote spans markup the
 * text doesn't show).
 */
export function findQuoteRange(root: Node, quote: string): Range | undefined {
	const wanted = quote.replace(/\s+/g, " ").trim();
	if (!wanted) return undefined;
	const doc = root.ownerDocument ?? (root as Document);
	const walker = doc.createTreeWalker(root, 4 /* NodeFilter.SHOW_TEXT */);
	// The text with whitespace runs collapsed, and where each kept character came from.
	let flat = "";
	const origin: Array<{ node: Text; offset: number }> = [];
	let lastSpace = true;
	for (let node = walker.nextNode() as Text | null; node; node = walker.nextNode() as Text | null) {
		const data = node.data;
		for (let index = 0; index < data.length; index += 1) {
			const char = data[index]!;
			if (/\s/.test(char)) {
				if (lastSpace) continue;
				flat += " ";
				lastSpace = true;
			} else {
				flat += char;
				lastSpace = false;
			}
			origin.push({ node, offset: index });
		}
	}
	const at = flat.indexOf(wanted);
	if (at < 0) return undefined;
	const start = origin[at];
	const end = origin[at + wanted.length - 1];
	if (!start || !end) return undefined;
	const range = doc.createRange();
	range.setStart(start.node, start.offset);
	range.setEnd(end.node, end.offset + 1);
	return range;
}

/** Whether a point on screen falls inside a range (a click on a highlighted passage). */
function pointInRange(range: Range, x: number, y: number): boolean {
	const doc = range.startContainer.ownerDocument;
	if (!doc) return false;
	const anyDoc = doc as Document & {
		caretPositionFromPoint?: (x: number, y: number) => { offsetNode: Node; offset: number } | null;
		caretRangeFromPoint?: (x: number, y: number) => Range | null;
	};
	const position = anyDoc.caretPositionFromPoint?.(x, y);
	if (position) return range.isPointInRange(position.offsetNode, position.offset);
	const caret = anyDoc.caretRangeFromPoint?.(x, y);
	return caret ? range.isPointInRange(caret.startContainer, caret.startOffset) : false;
}

/** The longest passage a comment can quote. */
export const COMMENT_QUOTE_MAX = 2000;

/** Splits text into words in the reader's language (CJK included), where the runtime can. */
const wordSegmenter = typeof Intl !== "undefined" && "Segmenter" in Intl ? new Intl.Segmenter(undefined, { granularity: "word" }) : undefined;

/** The word a position falls inside (strictly: not at its edges), as [start, end) in the text. */
function wordAround(text: string, offset: number, side: "start" | "end"): [number, number] | undefined {
	if (!wordSegmenter || offset <= 0 || offset >= text.length) return undefined;
	// The word holding the character after the edge (for a start) or before it (for an end).
	const found = wordSegmenter.segment(text).containing(side === "start" ? offset : offset - 1);
	if (!found?.isWordLike) return undefined;
	const end = found.index + found.segment.length;
	return found.index < offset && offset < end ? [found.index, end] : undefined;
}

/**
 * The selection grown to whole words: a drag that stops inside a word
 * ("…I can spa") quotes the whole word ("…I can spawn"), as Medium and
 * Kindle highlights do. Only an edge inside a word moves.
 */
export function wholeWordRange(range: Range): Range {
	const grown = range.cloneRange();
	const { startContainer, startOffset, endContainer, endOffset } = range;
	if (startContainer.nodeType === 3 /* Node.TEXT_NODE */) {
		const word = wordAround((startContainer as Text).data, startOffset, "start");
		if (word) grown.setStart(startContainer, word[0]);
	}
	if (endContainer.nodeType === 3 /* Node.TEXT_NODE */) {
		const word = wordAround((endContainer as Text).data, endOffset, "end");
		if (word) grown.setEnd(endContainer, word[1]);
	}
	return grown;
}

/**
 * The message a selection lies in, as a new comment on that passage: only
 * when it starts and ends in the same commentable message, and isn't too
 * long to quote. The passage is the selection grown to whole words.
 */
export function commentDraftForSelection(range: Range): CommentDraft | undefined {
	const anchorOf = (node: Node) => (node instanceof Element ? node : node.parentElement)?.closest<HTMLElement>("[data-comment-anchor]") ?? undefined;
	const start = anchorOf(range.startContainer);
	if (!start || start !== anchorOf(range.endContainer)) return undefined;
	const sequence = Number(start.dataset.commentAnchor);
	const kind = start.dataset.commentKind;
	if (!Number.isFinite(sequence) || (kind !== "user_message" && kind !== "steer" && kind !== "agent_message")) return undefined;
	const quote = wholeWordRange(range).toString().replace(/\s+/g, " ").trim();
	if (!quote || quote.length > COMMENT_QUOTE_MAX) return undefined;
	const body = start.querySelector("[data-chat-message-body]") ?? start;
	const authorName = start.dataset.commentAuthor;
	return { anchor: { sequence, kind }, quote, excerpt: messageExcerpt(body.textContent ?? ""), ...(authorName ? { authorName } : {}) };
}

/**
 * The first line of a message's text, to show what a comment is about. A
 * markdown code fence ("```python") is markup, not a line: an answer that
 * starts with code shows its first line of code.
 */
export function messageExcerpt(text: string): string {
	const line = text.split(/\r?\n/).find((candidate) => candidate.trim() !== "" && !/^\s*(```|~~~)/.test(candidate))?.trim() ?? "";
	return line.length > 200 ? `${line.slice(0, 199).trimEnd()}…` : line;
}

/**
 * A message the reader can comment on. With the conversation's comments it
 * adds the hover Comment button (finished messages only), the margin icon of
 * its open threads, and the highlights of the passages they quote; without
 * them it draws the message alone.
 */
export function CommentTarget(props: {
	/** The message's anchor: its event, or for the agent's answer its first output event. */
	sequence: number | undefined;
	kind: CommentDraft["anchor"]["kind"];
	/** The aside the message's tags started, if any. */
	asideId?: string;
	/** The message's text, for the new comment's excerpt. */
	text: string;
	authorName?: string;
	/** The message is complete: not streaming, queued or being edited. */
	finished: boolean;
	children: ReactNode;
}) {
	const markers = useContext(CommentMarkersContext);
	if (!markers || props.sequence === undefined) return <>{props.children}</>;
	return <CommentTargetWith markers={markers} {...props} sequence={props.sequence} />;
}

function CommentTargetWith({
	markers,
	sequence,
	kind,
	asideId,
	text,
	authorName,
	finished,
	children,
}: {
	markers: CommentMarkers;
	sequence: number;
	kind: CommentDraft["anchor"]["kind"];
	asideId?: string;
	text: string;
	authorName?: string;
	finished: boolean;
	children: ReactNode;
}) {
	const { t } = useTranslation();
	const key = useId();
	const root = useRef<HTMLDivElement>(null);
	const ranges = useRef<Array<{ asideId: string; range: Range }>>([]);
	const comments = markers.forMessage(sequence, asideId);
	// Paint after every render: the message's text can change under the same
	// quotes (the rest of an answer arriving, its markdown drawn again).
	useLayoutEffect(() => {
		const node = root.current;
		const body = node?.querySelector("[data-chat-message-body]") ?? node;
		const found: Array<{ asideId: string; range: Range; active: boolean }> = [];
		if (body) {
			for (const quote of comments?.quotes ?? []) {
				const range = findQuoteRange(body, quote.quote);
				if (range) found.push({ asideId: quote.asideId, range, active: quote.active });
			}
		}
		ranges.current = found;
		if (found.length) {
			painted.set(key, {
				ranges: found.filter((entry) => !entry.active).map((entry) => entry.range),
				active: found.filter((entry) => entry.active).map((entry) => entry.range),
			});
			repaint();
		} else if (painted.delete(key)) {
			repaint();
		}
	});
	useLayoutEffect(() => () => {
		if (painted.delete(key)) repaint();
	}, [key]);

	const onClick = (event: MouseEvent<HTMLDivElement>) => {
		if (ranges.current.length === 0 || event.defaultPrevented) return;
		const selection = window.getSelection();
		if (selection && !selection.isCollapsed) return;
		const hit = ranges.current.find((entry) => pointInRange(entry.range, event.clientX, event.clientY));
		if (hit) markers.openThread(hit.asideId);
	};

	const markerLabel = comments?.state === "active" ? t("comment.closeMarker") : comments?.state === "unread" ? t("comment.openUnread") : t("comment.open");
	return (
		<div className="group/comment relative" data-comment-anchor={sequence} data-comment-author={authorName} data-comment-kind={kind} onClick={onClick} ref={root}>
			{children}
			{finished && markers.canComment ? (
				// In the right margin, as a document's add-comment button: it never
				// covers the message's text, and sits under the comment icon when the
				// message has one.
				<div
					className={cn(
						"absolute -right-9 z-20 flex rounded-[9px] border border-border bg-popover opacity-0 shadow-sm transition-opacity duration-150 ease-out focus-within:opacity-100 group-hover/comment:opacity-100 motion-reduce:transition-none",
						comments ? "top-8" : "top-0",
					)}
					data-comment-toolbar=""
				>
					<Tooltip>
						<TooltipTrigger asChild>
							<Button
								aria-label={t("comment.action")}
								onClick={(event) => {
									event.stopPropagation();
									markers.startComment({ anchor: { sequence, kind }, excerpt: messageExcerpt(text), ...(authorName ? { authorName } : {}) });
								}}
								size="icon-sm"
								type="button"
								variant="quiet"
							>
								<MessageSquarePlus aria-hidden="true" className="size-icon-base" />
							</Button>
						</TooltipTrigger>
						<TooltipContent side="bottom">{t("comment.action")}</TooltipContent>
					</Tooltip>
				</div>
			) : null}
			{comments ? (
				<Button
					aria-label={markerLabel}
					aria-pressed={comments.state === "active"}
					className={cn(
						"absolute top-0 -right-9 size-7",
						comments.state === "idle" && "text-muted-foreground",
						comments.state === "unread" && "text-status-working",
						comments.state === "active" && "bg-raised text-foreground",
					)}
					data-comment-marker={comments.state}
					onClick={(event) => {
						event.stopPropagation();
						markers.openMessage(sequence, comments);
					}}
					size="icon-sm"
					type="button"
					variant="quiet"
				>
					<MessageSquare aria-hidden="true" className="size-icon-base" />
				</Button>
			) : null}
		</div>
	);
}
