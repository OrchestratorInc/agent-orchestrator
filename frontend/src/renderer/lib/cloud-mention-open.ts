import { useSyncExternalStore } from "react";
import type { QueryClient } from "@tanstack/react-query";
import {
	CHAT_EVENTS_CACHE_MS,
	cloudChatEventsKey,
	cloudChatHistoryKey,
	loadCloudChatHistory,
	loadOlderCloudChatEvents,
	type CloudChatHistory,
} from "./cloud-chat-history";
import type { CloudCpClient } from "./cloud-cp/client";
import type { CloudCpClientEvent, CloudCpNotification, CloudCpSession } from "./cloud-cp/types";
import { asideReplyMetadata, mentionMetadata } from "./cloud-notifications";
import { prefetchAside } from "./cloud-asides";

// Opening a mention is instant: when a mention notification arrives, the
// session it points at is read into the React Query cache (its header, the
// conversation around the message, its row in the sidebar), so a click on
// the bell row or the desktop alert draws the session from that cache in the
// same frame, at the message, with no loading state. The live stream connects
// after the click and only adds what is newer. With nothing cached (after a
// restart, say) the session still opens at once, showing the message from
// the notification's own text until the rest loads.
//
// A mention or a reply in an aside opens the same way, with the aside open
// beside the conversation and its reply box focused; the aside's replies are
// read into the cache on arrival too.

/** Where a mention or aside reply notification opens: the session, at the message, with its aside open. */
export interface MentionOpenTarget {
	notificationId: string;
	/** A mention, or a reply in an aside (whose body is the reply, not the message). */
	kind: "mention" | "aside_reply";
	orgId: string;
	projectId?: string;
	sessionId: string;
	/** The message's (or steer's) event; undefined on a row without one, which opens the session as usual. */
	sequence?: number;
	turnId?: string;
	authorUserId?: string;
	authorName?: string;
	/** The notification's own text: the message's (or the reply's) first line, with each mention as @Name. */
	body: string;
	/** The team that named them, when only a team did: its chip is tinted. */
	teamId?: string;
	/** The aside to open beside the conversation; absent from a control plane without asides. */
	asideId?: string;
	createdAt: string;
}

/** A mention or aside reply notification's open target, or undefined for any other row or one without a session. */
export function mentionOpenTarget(row: CloudCpNotification): MentionOpenTarget | undefined {
	if (!row.sessionId) return undefined;
	const mention = mentionMetadata(row);
	const reply = mention ? undefined : asideReplyMetadata(row);
	const metadata = mention ?? reply;
	if (!metadata) return undefined;
	return {
		notificationId: row.id,
		kind: mention ? "mention" : "aside_reply",
		orgId: row.orgId,
		projectId: row.projectId,
		sessionId: row.sessionId,
		sequence: metadata.sequence,
		turnId: metadata.turnId,
		authorUserId: metadata.authorUserId,
		authorName: metadata.authorName,
		body: row.body,
		teamId: mention?.teamId,
		...(metadata.asideId ? { asideId: metadata.asideId } : {}),
		createdAt: row.updatedAt || row.createdAt,
	};
}

/** The chat timeline's id for a Cloud user message (see the chat surface's projection). */
export function mentionMessageId(sequence: number): string {
	return `cloud-event-${sequence}`;
}

// ---------------------------------------------------------------------------
// The open request: the session view picks it up and scrolls to the message.
// ---------------------------------------------------------------------------

export interface MentionFocus extends MentionOpenTarget {
	/** A new value for each open, so opening the same mention twice scrolls twice. */
	nonce: number;
}

let focus: MentionFocus | undefined;
let nonce = 0;
const focusListeners = new Set<() => void>();

function emitFocus(): void {
	for (const listener of focusListeners) listener();
}

/** Asks the session view to open at this mention's message; replaces any earlier request. */
export function requestMentionFocus(target: MentionOpenTarget): MentionFocus {
	nonce += 1;
	focus = { ...target, nonce };
	emitFocus();
	return focus;
}

/** The open request for a session, once handled (or given up). Only the request asked about is cleared. */
export function clearMentionFocus(sessionId: string, requestNonce: number): void {
	if (!focus || focus.sessionId !== sessionId || focus.nonce !== requestNonce) return;
	focus = undefined;
	emitFocus();
}

export function peekMentionFocus(): MentionFocus | undefined {
	return focus;
}

function subscribeFocus(listener: () => void): () => void {
	focusListeners.add(listener);
	return () => { focusListeners.delete(listener); };
}

/** The pending open request for this session, if any. */
export function useMentionFocus(sessionId: string | undefined): MentionFocus | undefined {
	return useSyncExternalStore(subscribeFocus, () => (sessionId && focus?.sessionId === sessionId ? focus : undefined));
}

/**
 * The message as the notification describes it, for a session opened with
 * nothing cached: one user message at the notification's sequence, so the
 * view shows it (and scrolls to it) at once. The real event has the same
 * sequence, so it takes the row's place when the conversation loads.
 */
export function mentionFallbackEvents(target: MentionOpenTarget | undefined): CloudCpClientEvent[] | undefined {
	// An aside reply's text is the reply, which belongs in the aside, not the conversation.
	if (!target || target.kind !== "mention" || target.sequence === undefined || target.body.trim() === "") return undefined;
	return [{
		sessionId: target.sessionId,
		sequence: target.sequence,
		type: "chat.user_message",
		payload: {
			text: target.body,
			...(target.authorUserId ? { authorUserId: target.authorUserId } : {}),
			...(target.authorName ? { authorName: target.authorName } : {}),
			...(target.asideId ? { asideId: target.asideId } : {}),
		},
		createdAt: target.createdAt,
	}];
}

// ---------------------------------------------------------------------------
// Prefetch on arrival.
// ---------------------------------------------------------------------------

/** How many older pages a prefetch reads, at most, to reach a message above the newest page. */
const MAX_OLDER_PAGES = 3;

/** The session lookup the session view reads when its row is not listed yet (useCloudSessionQuery). */
export const cloudSessionQueryKey = (baseUrl: string, orgId: string, sessionId: string) =>
	["cloud-session", baseUrl, orgId, sessionId] as const;

/**
 * Reads the session a mention points at into the cache: its header (the
 * session lookup) and the conversation around the message (the chat's own
 * event list, the same read the chat makes, plus older pages until the
 * message is in it). The sidebar row comes from the session lists, which the
 * notification stream refreshes on every event. Failures are left for the
 * chat to retry when it opens.
 */
export async function prefetchMentionedSession(
	queryClient: QueryClient,
	client: Pick<CloudCpClient, "getSession" | "listChatEvents"> & Partial<Pick<CloudCpClient, "getSessionAside">>,
	baseUrl: string,
	target: MentionOpenTarget,
): Promise<void> {
	const { orgId, sessionId, sequence, asideId } = target;
	// The aside opens beside the conversation with its replies already here.
	const aside = asideId && client.getSessionAside
		? prefetchAside(queryClient, { getSessionAside: client.getSessionAside }, orgId, sessionId, asideId, CHAT_EVENTS_CACHE_MS)
		: Promise.resolve();
	const header = queryClient.prefetchQuery({
		queryKey: cloudSessionQueryKey(baseUrl, orgId, sessionId),
		queryFn: async (): Promise<CloudCpSession | undefined> => (await client.getSession(orgId, sessionId)).session,
	});
	const conversation = (async () => {
		await queryClient.prefetchQuery({
			queryKey: cloudChatEventsKey(orgId, sessionId),
			queryFn: () => loadCloudChatHistory(queryClient, client, orgId, sessionId),
			gcTime: CHAT_EVENTS_CACHE_MS,
		});
		if (sequence === undefined) return;
		for (let page = 0; page < MAX_OLDER_PAGES; page += 1) {
			const events = queryClient.getQueryData<CloudCpClientEvent[]>(cloudChatEventsKey(orgId, sessionId));
			const history = queryClient.getQueryData<CloudChatHistory>(cloudChatHistoryKey(orgId, sessionId));
			if (!events?.length || !history?.hasOlder || events[0]!.sequence <= sequence) return;
			await loadOlderCloudChatEvents(queryClient, client, orgId, sessionId);
		}
	})();
	await Promise.allSettled([header, conversation, aside]);
}
