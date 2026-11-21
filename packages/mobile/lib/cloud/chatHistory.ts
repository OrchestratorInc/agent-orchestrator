import { forgetOnCloudAccountChange } from "./accountCaches";
import type { CloudClient } from "./client";
import { appendCloudEvents } from "./conversation";
import type { CloudChatEventsPage, CloudClientEvent } from "./types";

/**
 * A Cloud chat reads its history newest first, as the desktop does
 * (cloud-chat-history.ts): the newest page, then the half hour before it, and
 * older pages only when the reader asks for them. The control plane starts
 * each page where the turns it selects start (the turn's prompt). What that
 * reads in can begin partway through an earlier turn (a prompt queued while it
 * ran), which shows from there until older pages load.
 */

/** The before cursor that reads the newest page. */
export const CLOUD_CHAT_NEWEST = 9_007_199_254_740_991;
/** Events in the first page a chat shows. */
export const CLOUD_CHAT_LATEST_PAGE = 150;
/** How far back from the first page the second read goes. */
export const CLOUD_CHAT_RECENT_MS = 30 * 60_000;
/** Events in a page read on demand. */
export const CLOUD_CHAT_OLDER_PAGE = 200;
/** Events a read after the newest held one, or of the recent half hour, takes at most. */
const CLOUD_CHAT_WIDE_PAGE = 500;

type ChatEventsClient = Pick<CloudClient, "listChatEvents">;

/**
 * What the held event list lacks of the session's history. Undefined: nothing,
 * the list runs from the session's start (a control plane that cannot read
 * back, or a chat read back to its start).
 */
export type CloudChatHistory = {
	/** Events older than the list's first remain on the control plane. */
	hasOlder: boolean;
	/** The before cursor of the next older page. */
	nextBefore: number;
	/** The half hour before the first page is still to be read. */
	recentPending: boolean;
};

/** A control plane that predates reading back answers a before read forward, without hasOlder. */
function readBack(page: CloudChatEventsPage): page is CloudChatEventsPage & { hasOlder: boolean } {
	return typeof page.hasOlder === "boolean";
}

/** Reads forward from a page until the control plane has no more: how an older control plane is read. */
async function readForward(client: ChatEventsClient, orgId: string, sessionId: string, events: CloudClientEvent[], page: CloudChatEventsPage, after: number): Promise<CloudClientEvent[]> {
	events = appendCloudEvents(events, page.events ?? []);
	while (page.hasMore) {
		if (page.nextAfter <= after) throw new Error("Cloud event cursor did not advance.");
		after = page.nextAfter;
		page = await client.listChatEvents(orgId, sessionId, { after, limit: CLOUD_CHAT_WIDE_PAGE });
		events = appendCloudEvents(events, page.events ?? []);
	}
	return events;
}

/** The result of one read of a chat's events. */
export type CloudChatRead = {
	events: CloudClientEvent[];
	/** The list starts over at `events`, lacking what `history` says (undefined: nothing). */
	startOver?: { history: CloudChatHistory | undefined };
};

/** The newest page, turn aligned; an older control plane reads the whole history forward instead. */
async function readLatest(client: ChatEventsClient, orgId: string, sessionId: string): Promise<CloudChatRead> {
	const page = await client.listChatEvents(orgId, sessionId, { before: CLOUD_CHAT_NEWEST, limit: CLOUD_CHAT_LATEST_PAGE });
	if (!readBack(page)) return { events: await readForward(client, orgId, sessionId, [], page, 0), startOver: { history: undefined } };
	return {
		events: page.events ?? [],
		startOver: {
			history: page.hasOlder
				? { hasOlder: true, nextBefore: page.nextBefore ?? page.events?.[0]?.sequence ?? CLOUD_CHAT_NEWEST, recentPending: true }
				: undefined,
		},
	};
}

/**
 * One read of a chat's events. With nothing held it reads the newest page.
 * Otherwise it reads what came after the newest held event; when more came
 * than one page holds, the events between are unknown, and older pages load
 * below the list, so nothing would fill them: the list starts over at the
 * newest page instead.
 */
export async function readCloudChatEvents(client: ChatEventsClient, orgId: string, sessionId: string, held: readonly CloudClientEvent[]): Promise<CloudChatRead> {
	if (!held.length) return readLatest(client, orgId, sessionId);
	const after = held.at(-1)!.sequence;
	const page = await client.listChatEvents(orgId, sessionId, { after, before: CLOUD_CHAT_NEWEST, limit: CLOUD_CHAT_WIDE_PAGE });
	if (!readBack(page)) return { events: await readForward(client, orgId, sessionId, [...held], page, after) };
	if (page.hasOlder) return readLatest(client, orgId, sessionId);
	return { events: appendCloudEvents(held, page.events ?? []) };
}

/** What an older page read adds: the page's events, and what the list then lacks. `restart`: the control plane stopped reading back. */
export type CloudChatOlderRead =
	| { restart: true }
	| { restart?: false; older: CloudClientEvent[]; history: CloudChatHistory };

/**
 * Reads the page before the list's first event: the half hour before the
 * first page (`recent`), or the next older page. The caller puts it in front
 * of the list as it is by then (prependOlderCloudEvents).
 */
export async function readOlderCloudChatEvents(
	client: ChatEventsClient,
	orgId: string,
	sessionId: string,
	held: readonly CloudClientEvent[],
	history: CloudChatHistory,
	{ recent = false }: { recent?: boolean } = {},
): Promise<CloudChatOlderRead> {
	const oldestAt = Date.parse(held[0]?.createdAt ?? "");
	const since = recent && Number.isFinite(oldestAt) ? new Date(oldestAt - CLOUD_CHAT_RECENT_MS).toISOString() : undefined;
	const page = await client.listChatEvents(orgId, sessionId, { before: history.nextBefore, limit: recent ? CLOUD_CHAT_WIDE_PAGE : CLOUD_CHAT_OLDER_PAGE, since });
	// The control plane no longer reads back (a rollback): the whole history is read again, forward.
	if (!readBack(page)) return { restart: true };
	return {
		older: page.events ?? [],
		history: {
			hasOlder: page.hasOlder,
			nextBefore: page.nextBefore ?? page.events?.[0]?.sequence ?? history.nextBefore,
			recentPending: recent ? false : history.recentPending,
		},
	};
}

/** An older page in front of the list as it is now: only what is below its first event. */
export function prependOlderCloudEvents(held: CloudClientEvent[], older: readonly CloudClientEvent[]): CloudClientEvent[] {
	const first = held[0]?.sequence ?? Infinity;
	const front = older.filter((event) => event.sequence < first).sort((a, b) => a.sequence - b.sequence);
	return front.length ? [...front, ...held] : held;
}

/**
 * Streamed events the list can take: none below its first event while older
 * history remains, where they would leave a hole older pages cannot fill.
 */
export function streamedCloudChatEvents(held: readonly CloudClientEvent[], history: CloudChatHistory | undefined, incoming: CloudClientEvent[]): CloudClientEvent[] {
	const first = held[0]?.sequence;
	if (first === undefined || !history?.hasOlder) return incoming;
	return incoming.filter((event) => event.sequence >= first);
}

// A chat left a moment ago reopens from what it held, and reads only what is
// newer: the desktop keeps a closed Cloud chat for an hour. A few chats, so a
// long one's events do not linger in memory for every session opened.
const RETAIN_MS = 60 * 60_000;
const MAX_RETAINED = 8;
const retained = new Map<string, { at: number; events: CloudClientEvent[]; history: CloudChatHistory | undefined }>();

export function cloudChatKey(baseUrl: string, orgId: string, sessionId: string): string {
	return JSON.stringify([baseUrl, orgId, sessionId]);
}

/** What a chat held when it was last open, within the hour. */
export function retainedCloudChat(key: string): { events: CloudClientEvent[]; history: CloudChatHistory | undefined } | undefined {
	const entry = retained.get(key);
	if (!entry) return undefined;
	if (Date.now() - entry.at > RETAIN_MS) {
		retained.delete(key);
		return undefined;
	}
	return entry;
}

export function retainCloudChat(key: string, events: CloudClientEvent[], history: CloudChatHistory | undefined): void {
	retained.delete(key);
	retained.set(key, { at: Date.now(), events, history });
	if (retained.size > MAX_RETAINED) retained.delete(retained.keys().next().value!);
}

/** Forgets every held chat: on sign-out and an account switch, so the next account never reads the last one's. */
export function clearCloudChats(): void {
	retained.clear();
}
forgetOnCloudAccountChange(clearCloudChats);
