import { useSyncExternalStore } from "react";
import type { QueryClient } from "@tanstack/react-query";
import type { CloudCpClient } from "./cloud-cp/client";
import type {
	CloudCpAside,
	CloudCpAsideAnchorKind,
	CloudCpAsideListResponse,
	CloudCpAsidePerson,
	CloudCpAsideReply,
	CloudCpAsideThread,
	CloudCpClientEvent,
	CloudCpFromAside,
} from "./cloud-cp/types";
import { parseEventMentions } from "./cloud-mentions";

// Asides in shared Cloud sessions. A message (or steer) that mentions people
// or teams starts an aside: a thread between people about that message, in a
// pane beside the conversation. The message still goes to the agent like any
// other; the agent never reads the aside. Its event names it by asideId, which
// the control plane sets only once it has asides, so an aside icon is never
// drawn for a control plane that can't open one.

/** A reply in an aside, on the session's event stream (never a worker's). */
export const ASIDE_REPLY_EVENT = "chat.aside_reply";

/**
 * Events that change a session's comment threads (asides), never a worker's:
 * a thread started, resolved, reopened or deleted, a comment edited or
 * deleted. Any of them means the list of threads (markers, highlights) is
 * read again.
 */
export const ASIDE_STATE_EVENTS = new Set([
	"chat.aside_created",
	"chat.aside_resolved",
	"chat.aside_reopened",
	"chat.aside_reply_edited",
	"chat.aside_reply_deleted",
	"chat.aside_deleted",
]);

/** A comment edit on the stream, for a thread on screen. */
export interface AsideReplyEdit {
	asideId: string;
	replyId: string;
	text: string;
	mentions: ReturnType<typeof parseEventMentions>;
	editedAt: string;
}

/** A chat.aside_reply_edited event as the edit it carries. */
export function asideReplyEditOf(event: Pick<CloudCpClientEvent, "type" | "payload" | "createdAt">): AsideReplyEdit | undefined {
	if (event.type !== "chat.aside_reply_edited") return undefined;
	const payload = event.payload as Record<string, unknown> | null | undefined;
	if (!payload || typeof payload.asideId !== "string" || typeof payload.replyId !== "string") return undefined;
	return {
		asideId: payload.asideId,
		replyId: payload.replyId,
		text: typeof payload.text === "string" ? payload.text : "",
		mentions: parseEventMentions(payload.mentions),
		editedAt: typeof payload.editedAt === "string" ? payload.editedAt : event.createdAt,
	};
}

/** The comment a chat.aside_reply_deleted event removed, or the thread a chat.aside_deleted removed. */
export function asideRemovalOf(event: Pick<CloudCpClientEvent, "type" | "payload">): { asideId: string; replyId?: string } | undefined {
	if (event.type !== "chat.aside_reply_deleted" && event.type !== "chat.aside_deleted") return undefined;
	const payload = event.payload as Record<string, unknown> | null | undefined;
	if (!payload || typeof payload.asideId !== "string") return undefined;
	return event.type === "chat.aside_deleted" ? { asideId: payload.asideId } : typeof payload.replyId === "string" ? { asideId: payload.asideId, replyId: payload.replyId } : undefined;
}

/** A person named on an event or thread, when well formed. */
function personOf(value: unknown): CloudCpAsidePerson | undefined {
	if (!value || typeof value !== "object") return undefined;
	const { userId, name } = value as { userId?: unknown; name?: unknown };
	return typeof userId === "string" && typeof name === "string" ? { userId, name } : undefined;
}

/** How a resolve or reopen on the stream changes a thread, for a pane open on it. */
export function asideStateChangeOf(event: Pick<CloudCpClientEvent, "type" | "payload" | "createdAt">): { asideId: string; patch: Partial<CloudCpAside> } | undefined {
	const payload = event.payload as Record<string, unknown> | null | undefined;
	if (!payload || typeof payload.asideId !== "string") return undefined;
	if (event.type === "chat.aside_resolved") {
		const resolvedBy = personOf(payload.resolvedBy);
		const resolvedAt = typeof payload.resolvedAt === "string" ? payload.resolvedAt : event.createdAt;
		return {
			asideId: payload.asideId,
			patch: {
				resolvedAt,
				...(resolvedBy ? { resolvedBy } : {}),
				...(payload.sentToAgent === true ? { sentToAgentAt: resolvedAt, ...(resolvedBy ? { sentToAgentBy: resolvedBy } : {}) } : {}),
			},
		};
	}
	if (event.type === "chat.aside_reopened") return { asideId: payload.asideId, patch: { resolvedAt: undefined, resolvedBy: undefined } };
	return undefined;
}

/** The thread a user message carries when "Also send to agent" sent it, or undefined. */
export function fromAsideOf(payload: Record<string, unknown> | null | undefined): CloudCpFromAside | undefined {
	const value = payload?.fromAside;
	if (!value || typeof value !== "object") return undefined;
	const raw = value as Record<string, unknown>;
	if (typeof raw.asideId !== "string" || !Array.isArray(raw.comments)) return undefined;
	const comments = raw.comments.flatMap((comment) => {
		if (!comment || typeof comment !== "object") return [];
		const { authorUserId, authorName, text } = comment as Record<string, unknown>;
		return typeof authorName === "string" && typeof text === "string"
			? [{ ...(typeof authorUserId === "string" ? { authorUserId } : {}), authorName, text }]
			: [];
	});
	const kind = raw.anchorKind;
	return {
		asideId: raw.asideId,
		...(typeof raw.quote === "string" && raw.quote !== "" ? { quote: raw.quote } : {}),
		...(kind === "user_message" || kind === "steer" || kind === "agent_message" ? { anchorKind: kind } : {}),
		...(typeof raw.anchorExcerpt === "string" && raw.anchorExcerpt !== "" ? { anchorExcerpt: raw.anchorExcerpt } : {}),
		comments,
	};
}

/** An open thread (not resolved) draws a marker and its highlight; a resolved one draws nothing. */
export function asideIsOpen(aside: Pick<CloudCpAside, "resolvedAt">): boolean {
	return !aside.resolvedAt;
}

/** At most this many replies one read of an aside takes, and this many reads. */
const ASIDE_PAGE = 200;
const ASIDE_MAX_PAGES = 10;

/** The session's asides as this person sees them (reply counts, unread dots, mute). */
export const cloudAsidesKey = (orgId: string, sessionId: string) => ["cloud-asides", orgId, sessionId] as const;

/** One aside: its message and replies. */
export const cloudAsideKey = (orgId: string, sessionId: string, asideId: string) => ["cloud-aside", orgId, sessionId, asideId] as const;

/** Events of a message or steer: they name the aside it started. */
const ASIDE_ANCHOR_EVENTS = new Set(["chat.user_message", "chat.turn_steered", "chat.turn_steer_requested"]);

/** The aside a message's or steer's event started, or undefined for any other event or a control plane without asides. */
export function asideIdOf(event: Pick<CloudCpClientEvent, "type" | "payload">): string | undefined {
	if (!ASIDE_ANCHOR_EVENTS.has(event.type)) return undefined;
	const value = (event.payload as { asideId?: unknown } | null | undefined)?.asideId;
	return typeof value === "string" && value !== "" ? value : undefined;
}

/** A chat.aside_reply event as the reply it carries; anything malformed is left out. */
export function asideReplyOf(event: Pick<CloudCpClientEvent, "type" | "payload" | "createdAt">): CloudCpAsideReply | undefined {
	if (event.type !== ASIDE_REPLY_EVENT) return undefined;
	const payload = event.payload as Record<string, unknown> | null | undefined;
	if (!payload || typeof payload !== "object") return undefined;
	const text = (key: string) => (typeof payload[key] === "string" ? payload[key] as string : undefined);
	const asideId = text("asideId");
	const replyId = text("replyId");
	if (!asideId || !replyId) return undefined;
	return {
		asideId,
		replyId,
		text: text("text") ?? "",
		mentions: parseEventMentions(payload.mentions),
		authorUserId: text("authorUserId") ?? "",
		authorName: text("authorName") ?? "",
		createdAt: text("createdAt") ?? event.createdAt,
	};
}

/** Each reply once (the newest copy of it wins), oldest first. */
export function mergeAsideReplies(...lists: ReadonlyArray<readonly CloudCpAsideReply[]>): CloudCpAsideReply[] {
	const byId = new Map<string, CloudCpAsideReply>();
	for (const list of lists) for (const reply of list) byId.set(reply.replyId, reply);
	return [...byId.values()]
		.map((reply, index) => ({ reply, index }))
		.sort((a, b) => (Date.parse(a.reply.createdAt) || 0) - (Date.parse(b.reply.createdAt) || 0) || a.index - b.index)
		.map(({ reply }) => reply);
}

/** Reads an aside with every reply (up to a bound), oldest first. */
export async function loadAsideThread(
	client: Pick<CloudCpClient, "getSessionAside">,
	orgId: string,
	sessionId: string,
	asideId: string,
	signal?: AbortSignal,
): Promise<CloudCpAsideThread> {
	const first = await client.getSessionAside(orgId, sessionId, asideId, { limit: ASIDE_PAGE }, { signal });
	let page = first;
	const replies = [...first.replies];
	for (let read = 1; page.hasMore && page.nextCursor && read < ASIDE_MAX_PAGES; read += 1) {
		page = await client.getSessionAside(orgId, sessionId, asideId, { cursor: page.nextCursor, limit: ASIDE_PAGE }, { signal });
		replies.push(...page.replies);
	}
	return { ...first, replies: mergeAsideReplies(replies), hasMore: page.hasMore, ...(page.nextCursor ? { nextCursor: page.nextCursor } : {}) };
}

/**
 * Reads an aside into the cache, so opening it from a notification shows its
 * replies at once. Failures are left for the pane to retry when it opens.
 */
export async function prefetchAside(
	queryClient: QueryClient,
	client: Pick<CloudCpClient, "getSessionAside">,
	orgId: string,
	sessionId: string,
	asideId: string,
	gcTime?: number,
): Promise<void> {
	await queryClient.prefetchQuery({
		queryKey: cloudAsideKey(orgId, sessionId, asideId),
		queryFn: ({ signal }) => loadAsideThread(client, orgId, sessionId, asideId, signal),
		...(gcTime ? { gcTime } : {}),
	});
}

/** Changes one aside in the cached list (and in its cached thread), if they are there. */
export function patchCachedAside(queryClient: QueryClient, orgId: string, sessionId: string, asideId: string, patch: Partial<CloudCpAside>): void {
	queryClient.setQueryData<CloudCpAsideListResponse>(cloudAsidesKey(orgId, sessionId), (list) => list && {
		...list,
		items: list.items.map((aside) => aside.asideId === asideId ? { ...aside, ...patch } : aside),
	});
	queryClient.setQueryData<CloudCpAsideThread>(cloudAsideKey(orgId, sessionId, asideId), (thread) => thread && {
		...thread,
		aside: { ...thread.aside, ...patch },
	});
}

// ---------------------------------------------------------------------------
// Which aside is open beside a session's conversation. One per session; a
// notification opens it before the session is drawn, so the pane is there in
// the first frame.
// ---------------------------------------------------------------------------

/** A comment not sent yet: what "Comment" opened the pane on. Nothing exists until its first comment is sent. */
export interface CommentDraft {
	anchor: { sequence: number; kind: CloudCpAsideAnchorKind };
	/** The selected passage, for a comment on part of the message. */
	quote?: string;
	/** The message's first line, to show what the comment is about. */
	excerpt: string;
	/** Who wrote the message; absent for the agent's answer. */
	authorName?: string;
}

/**
 * What the pane beside a session's conversation shows: one thread
 * (asideId), the threads of one message (a message's comment icon with
 * several), or a new comment (draft).
 */
export interface OpenAside {
	asideId?: string;
	/** The message whose threads are listed (its anchor sequence). */
	messageSequence?: number;
	draft?: CommentDraft;
	/** Changes on every open that should put the caret in the reply box. */
	focusKey?: number;
}

const openBySession = new Map<string, OpenAside>();
const listeners = new Set<() => void>();
let focusKey = 0;

function emit(): void {
	for (const listener of listeners) listener();
}

/** Opens an aside beside the session's conversation, with the reply box focused unless asked not to. */
export function openAside(sessionId: string, asideId: string, options: { focusReply?: boolean } = {}): void {
	focusKey += 1;
	openBySession.set(sessionId, { asideId, ...(options.focusReply === false ? {} : { focusKey }) });
	emit();
}

/** Opens a new comment on a message (or a passage of it); nothing is created until it is sent. */
export function openCommentDraft(sessionId: string, draft: CommentDraft): void {
	focusKey += 1;
	openBySession.set(sessionId, { draft, focusKey });
	emit();
}

/** Opens the list of one message's threads. */
export function openMessageComments(sessionId: string, messageSequence: number): void {
	openBySession.set(sessionId, { messageSequence });
	emit();
}

/** Closes the session's aside pane (only that aside, when one is named). */
export function closeAside(sessionId: string, asideId?: string): void {
	const open = openBySession.get(sessionId);
	if (!open || (asideId && open.asideId !== asideId)) return;
	openBySession.delete(sessionId);
	emit();
}

/** The aside icon: opens its aside, or closes it when it is the one open. */
export function toggleAside(sessionId: string, asideId: string): void {
	if (openBySession.get(sessionId)?.asideId === asideId) closeAside(sessionId);
	else openAside(sessionId, asideId);
}

export function peekOpenAside(sessionId: string): OpenAside | undefined {
	return openBySession.get(sessionId);
}

/** Test seam: close every aside. */
export function resetOpenAsides(): void {
	openBySession.clear();
	emit();
}

function subscribe(listener: () => void): () => void {
	listeners.add(listener);
	return () => { listeners.delete(listener); };
}

/** The aside open beside this session's conversation, if any. */
export function useOpenAside(sessionId: string | undefined): OpenAside | undefined {
	return useSyncExternalStore(subscribe, () => (sessionId ? openBySession.get(sessionId) : undefined));
}
