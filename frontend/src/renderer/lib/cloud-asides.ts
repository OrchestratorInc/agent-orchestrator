import { useSyncExternalStore } from "react";
import type { QueryClient } from "@tanstack/react-query";
import type { CloudCpClient } from "./cloud-cp/client";
import type {
	CloudCpAside,
	CloudCpAsideAnchorKind,
	CloudCpAsideHistoryEntry,
	CloudCpAsideListResponse,
	CloudCpAsidePerson,
	CloudCpAsideReply,
	CloudCpAsideThread,
	CloudCpClientEvent,
	CloudCpCommentThread,
	CloudCpCommentThreadPage,
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
	/** The edit's event, so a reader knows which version of the comment it shows. */
	sequence?: number;
}

/** A chat.aside_reply_edited event as the edit it carries. */
export function asideReplyEditOf(event: Pick<CloudCpClientEvent, "type" | "payload" | "createdAt"> & { sequence?: number }): AsideReplyEdit | undefined {
	if (event.type !== "chat.aside_reply_edited") return undefined;
	const payload = event.payload as Record<string, unknown> | null | undefined;
	if (!payload || typeof payload.asideId !== "string" || typeof payload.replyId !== "string") return undefined;
	return {
		asideId: payload.asideId,
		replyId: payload.replyId,
		text: typeof payload.text === "string" ? payload.text : "",
		mentions: parseEventMentions(payload.mentions),
		editedAt: typeof payload.editedAt === "string" ? payload.editedAt : event.createdAt,
		...(typeof event.sequence === "number" ? { sequence: event.sequence } : {}),
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
		const { authorUserId, authorName, text, createdAt } = comment as Record<string, unknown>;
		return typeof authorName === "string" && typeof text === "string"
			? [{ ...(typeof authorUserId === "string" ? { authorUserId } : {}), authorName, text, ...(typeof createdAt === "string" ? { createdAt } : {}) }]
			: [];
	});
	const kind = raw.anchorKind;
	const count = (value: unknown) => (typeof value === "number" && Number.isSafeInteger(value) && value >= 0 ? value : undefined);
	const commentCount = count(raw.commentCount);
	const omittedComments = count(raw.omittedComments);
	return {
		asideId: raw.asideId,
		...(typeof raw.quote === "string" && raw.quote !== "" ? { quote: raw.quote } : {}),
		...(kind === "user_message" || kind === "steer" || kind === "agent_message" ? { anchorKind: kind } : {}),
		...(typeof raw.anchorExcerpt === "string" && raw.anchorExcerpt !== "" ? { anchorExcerpt: raw.anchorExcerpt } : {}),
		...(typeof raw.requestText === "string" && raw.requestText !== "" ? { requestText: raw.requestText } : {}),
		...(raw.sinceSent === true ? { sinceSent: true } : {}),
		...(typeof raw.previouslySentAt === "string" ? { previouslySentAt: raw.previouslySentAt } : {}),
		...(commentCount !== undefined ? { commentCount } : {}),
		...(omittedComments ? { omittedComments } : {}),
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

/** Every page of the threads view, for invalidating them all. */
export const cloudCommentThreadsRootKey = (baseUrl: string, userId: string) => ["cloud-comment-threads", baseUrl, userId] as const;

/** One list of the threads view: its tab (open or resolved) and Unread only. */
export const cloudCommentThreadsKey = (baseUrl: string, userId: string, status: "open" | "resolved", unread: boolean) =>
	[...cloudCommentThreadsRootKey(baseUrl, userId), status, unread] as const;

/** The Threads badge: open threads unread for this person. */
export const cloudUnreadThreadsKey = (baseUrl: string, userId: string) => ["cloud-comment-threads-unread", baseUrl, userId] as const;

/** The threads view's cached pages, as an infinite query keeps them. */
export interface CommentThreadPages {
	pages: CloudCpCommentThreadPage[];
	pageParams: unknown[];
}

/** Changes one thread's row wherever the threads view has it cached (read, unread, muted). */
export function patchCommentThreadRows(
	queryClient: QueryClient,
	baseUrl: string,
	userId: string,
	asideId: string,
	patch: Partial<Pick<CloudCpCommentThread, "unread" | "muted">>,
): void {
	queryClient.setQueriesData<CommentThreadPages>({ queryKey: cloudCommentThreadsRootKey(baseUrl, userId) }, (data) => data && {
		...data,
		pages: data.pages.map((page) => ({
			...page,
			items: page.items.map((row) => row.aside.asideId === asideId ? { ...row, ...patch, aside: { ...row.aside, ...patch } } : row),
		})),
	});
}

/**
 * A thread changed for this person (a comment, resolve, reopen, read or
 * unread): the threads view and its badge read again.
 */
export function invalidateCommentThreads(queryClient: QueryClient, baseUrl: string, userId: string): void {
	void queryClient.invalidateQueries({ queryKey: cloudCommentThreadsRootKey(baseUrl, userId) });
	void queryClient.invalidateQueries({ queryKey: cloudUnreadThreadsKey(baseUrl, userId) });
}

/** Events of a message or steer: they name the aside it started. */
const ASIDE_ANCHOR_EVENTS = new Set(["chat.user_message", "chat.turn_steered", "chat.turn_steer_requested"]);

/** The aside a message's or steer's event started, or undefined for any other event or a control plane without asides. */
export function asideIdOf(event: Pick<CloudCpClientEvent, "type" | "payload">): string | undefined {
	if (!ASIDE_ANCHOR_EVENTS.has(event.type)) return undefined;
	const value = (event.payload as { asideId?: unknown } | null | undefined)?.asideId;
	return typeof value === "string" && value !== "" ? value : undefined;
}

/** A chat.aside_reply event as the reply it carries; anything malformed is left out. */
export function asideReplyOf(event: Pick<CloudCpClientEvent, "type" | "payload" | "createdAt"> & { sequence?: number }): CloudCpAsideReply | undefined {
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
		// Its event orders it among the thread's resolve, reopen and send rows.
		...(typeof event.sequence === "number" ? { sequence: event.sequence } : {}),
	};
}

/** A copy of a reply's revision: its newest edit's event, else its own (0 without either). */
export function replyVersion(reply: Pick<CloudCpAsideReply, "sequence" | "editedSequence">): number {
	return reply.editedSequence ?? reply.sequence ?? 0;
}

/**
 * Each reply once, oldest first. Of its copies (the thread as read, the
 * stream, this person's own sends and edits) the newest revision wins
 * (replyVersion), the later list between equal ones: a copy kept locally
 * never hides a newer edit. Replies with session events are ordered by them,
 * any other by time.
 */
export function mergeAsideReplies(...lists: ReadonlyArray<readonly CloudCpAsideReply[]>): CloudCpAsideReply[] {
	const byId = new Map<string, CloudCpAsideReply>();
	for (const list of lists) {
		for (const reply of list) {
			const held = byId.get(reply.replyId);
			if (!held || replyVersion(reply) >= replyVersion(held)) byId.set(reply.replyId, reply);
		}
	}
	const time = (reply: CloudCpAsideReply) => Date.parse(reply.createdAt) || 0;
	return [...byId.values()]
		.map((reply, index) => ({ reply, index }))
		.sort((a, b) => (a.reply.sequence !== undefined && b.reply.sequence !== undefined ? a.reply.sequence - b.reply.sequence : time(a.reply) - time(b.reply)) || a.index - b.index)
		.map(({ reply }) => reply);
}

/** One row of a thread in the pane: a comment, or a state change ("{Name} resolved this"). */
export type ThreadRow<Reply> =
	| { kind: "comment"; reply: Reply }
	| { kind: "history"; entry: CloudCpAsideHistoryEntry };

/**
 * A thread's comments and its state changes in one order: by their session
 * events (sequence) when both have one, else by time, a comment before the
 * change it caused at the same time (a reply that reopened the thread, the
 * request of a send). Comments keep their own order among themselves.
 */
export function threadTimeline<Reply extends { createdAt: string; sequence?: number }>(
	replies: readonly Reply[],
	history: readonly CloudCpAsideHistoryEntry[] | undefined,
): Array<ThreadRow<Reply>> {
	const rows: Array<ThreadRow<Reply>> = replies.map((reply) => ({ kind: "comment", reply }));
	const entries = [...(history ?? [])].sort((a, b) => a.sequence - b.sequence || (Date.parse(a.at) || 0) - (Date.parse(b.at) || 0));
	for (const entry of entries) {
		// After every comment that comes before it.
		const at = Date.parse(entry.at) || 0;
		let index = rows.length;
		for (let candidate = 0; candidate < rows.length; candidate += 1) {
			const row = rows[candidate]!;
			const before = row.kind === "comment"
				? (row.reply.sequence !== undefined ? row.reply.sequence < entry.sequence : (Date.parse(row.reply.createdAt) || 0) <= at)
				: row.entry.sequence < entry.sequence;
			if (!before) {
				index = candidate;
				break;
			}
		}
		rows.splice(index, 0, { kind: "history", entry });
	}
	return rows;
}

/** The message the thread's last send to the agent became, to jump to. */
export function sentMessageSequence(aside: Pick<CloudCpAside, "sentMessage" | "history">): number | undefined {
	if (aside.sentMessage) return aside.sentMessage.sequence;
	const sent = [...(aside.history ?? [])].reverse().find((entry) => entry.kind === "sent_to_agent" && entry.sentMessage);
	return sent?.sentMessage?.sequence;
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

/**
 * Puts the control plane's answer (a whole Aside: resolve, reopen, read,
 * unread, mute, a reply) in the cache in place of what was there. Merging it
 * instead would keep a field the answer leaves out, such as the resolvedAt of
 * a thread a reply just reopened.
 */
export function replaceCachedAside(queryClient: QueryClient, orgId: string, sessionId: string, asideId: string, aside: CloudCpAside): void {
	queryClient.setQueryData<CloudCpAsideListResponse>(cloudAsidesKey(orgId, sessionId), (list) => list && {
		...list,
		items: list.items.map((item) => item.asideId === asideId ? aside : item),
	});
	queryClient.setQueryData<CloudCpAsideThread>(cloudAsideKey(orgId, sessionId, asideId), (thread) => thread && { ...thread, aside });
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
	/** What the person had already typed when the comment started (typing on a selection). */
	initialText?: string;
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

// ---------------------------------------------------------------------------
// The thread open in the threads view (the Threads page). One at a time; a
// notification opens it before the page is drawn.
// ---------------------------------------------------------------------------

/** A thread the threads view shows beside its list. */
export interface ThreadInView {
	orgId: string;
	sessionId: string;
	asideId: string;
	/** Its session's project, for "Open in session". */
	projectId?: string;
	sessionTitle?: string;
	/** The session ended: the thread is read-only. */
	ended?: boolean;
	/** The person may prompt the session's agent ("Also send to agent"). */
	canPrompt?: boolean;
	/** Changes on every open that should put the caret in the reply box. */
	focusKey?: number;
}

let threadInView: ThreadInView | undefined;
const threadListeners = new Set<() => void>();

/** Opens a thread in the threads view; the page shows it beside the list. */
export function openThreadInView(thread: Omit<ThreadInView, "focusKey">, options: { focusReply?: boolean } = {}): void {
	focusKey += 1;
	threadInView = { ...thread, ...(options.focusReply ? { focusKey } : {}) };
	for (const listener of threadListeners) listener();
}

export function closeThreadInView(): void {
	if (!threadInView) return;
	threadInView = undefined;
	for (const listener of threadListeners) listener();
}

export function peekThreadInView(): ThreadInView | undefined {
	return threadInView;
}

/** The thread open in the threads view, if any. */
export function useThreadInView(): ThreadInView | undefined {
	return useSyncExternalStore((listener) => {
		threadListeners.add(listener);
		return () => { threadListeners.delete(listener); };
	}, () => threadInView);
}
