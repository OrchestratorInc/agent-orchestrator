import { useSyncExternalStore } from "react";
import type { QueryClient } from "@tanstack/react-query";
import type { CloudCpClient } from "./cloud-cp/client";
import type {
	CloudCpAside,
	CloudCpAsideListResponse,
	CloudCpAsideReply,
	CloudCpAsideThread,
	CloudCpClientEvent,
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

export interface OpenAside {
	asideId: string;
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
