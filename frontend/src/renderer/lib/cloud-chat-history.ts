import type { QueryClient } from "@tanstack/react-query";
import type { CloudCpClient, CloudCpClientEvent, CloudCpChatEventsResponse } from "./cloud-cp";

/**
 * A Cloud chat reads its history newest first: the newest page, then the half
 * hour before it, and older pages only as the reader scrolls up. The control
 * plane starts each page where the turns it selects start (the turn's prompt).
 * What that reads in can begin partway through an earlier turn (a prompt
 * queued while it ran), which shows from there until older pages load.
 */

/** The before cursor that reads the newest page. */
export const CLOUD_CHAT_NEWEST = 9_007_199_254_740_991;
/** Events in the first page a chat shows. */
export const CLOUD_CHAT_LATEST_PAGE = 150;
/** How far back from the first page the second read goes. */
export const CLOUD_CHAT_RECENT_MS = 30 * 60_000;
/** Events in a page read on demand, as the reader scrolls up. */
export const CLOUD_CHAT_OLDER_PAGE = 200;
/** Events a read after the newest cached one, or of the recent half hour, takes at most. */
const CLOUD_CHAT_WIDE_PAGE = 500;

type ChatEventsClient = Pick<CloudCpClient, "listChatEvents">;

/**
 * What the cached event list holds of the session's history. No entry: the
 * whole history, from the session's start (a control plane that cannot read
 * back from a sequence, or a chat that has read back to its start).
 */
export interface CloudChatHistory {
	/** Events older than the list's first remain on the control plane. */
	hasOlder: boolean;
	/** The before cursor of the next older page. */
	nextBefore: number;
	/** The half hour before the first page is still to be read. */
	recentPending: boolean;
	/** An older page is being read. */
	loadingOlder: boolean;
	/** Changes each time the list starts over, so a page read for an earlier list is dropped. */
	generation: number;
}

export function cloudChatEventsKey(orgId: string, sessionId: string) {
	return ["cloud-chat-events", orgId, sessionId] as const;
}

export function cloudChatHistoryKey(orgId: string, sessionId: string) {
	return ["cloud-chat-history", orgId, sessionId] as const;
}

/** Drops every Cloud chat's cached events, history window and step log: on sign-out or an account switch. */
export function clearCloudChatCaches(queryClient: QueryClient): void {
	for (const key of ["cloud-chat-events", "cloud-chat-history", "cloud-script-steps"]) queryClient.removeQueries({ queryKey: [key] });
}

/** The cached list lacks the session's oldest events. */
export function cloudChatHistoryPartial(queryClient: QueryClient, orgId: string, sessionId: string): boolean {
	return Boolean(queryClient.getQueryData<CloudChatHistory>(cloudChatHistoryKey(orgId, sessionId))?.hasOlder);
}

/** A control plane that predates reading back answers a before read forward, without hasOlder. */
function readBack(page: CloudCpChatEventsResponse): page is CloudCpChatEventsResponse & { hasOlder: boolean } {
	return typeof page.hasOlder === "boolean";
}

/** Events after the list's newest, in order; a page that reaches back into the list adds only what it lacks. */
function appendNewer(events: CloudCpClientEvent[], incoming: readonly CloudCpClientEvent[]): CloudCpClientEvent[] {
	const newest = events.at(-1)?.sequence ?? 0;
	const newer = incoming.filter((event) => event.sequence > newest);
	return newer.length ? [...events, ...newer] : events;
}

/** Reads forward from a page until the control plane has no more: how an older control plane is read. */
async function readForward(client: ChatEventsClient, orgId: string, sessionId: string, events: CloudCpClientEvent[], page: CloudCpChatEventsResponse, after: number): Promise<CloudCpClientEvent[]> {
	events = appendNewer(events, page.events);
	while (page.hasMore) {
		if (page.nextAfter <= after) throw new Error("Cloud event cursor did not advance.");
		after = page.nextAfter;
		page = await client.listChatEvents(orgId, sessionId, { after, limit: CLOUD_CHAT_WIDE_PAGE });
		events = appendNewer(events, page.events);
	}
	return events;
}

/** The result of one read of a chat's events. */
export interface CloudChatRead {
	events: CloudCpClientEvent[];
	/** The list starts over at events: the history it describes, or undefined for the whole history. */
	startOver?: { history: Omit<CloudChatHistory, "generation" | "loadingOlder"> | undefined };
}

/** The newest page, turn aligned; an older control plane reads the whole history forward instead. */
async function readLatest(client: ChatEventsClient, orgId: string, sessionId: string): Promise<CloudChatRead> {
	const page = await client.listChatEvents(orgId, sessionId, { before: CLOUD_CHAT_NEWEST, limit: CLOUD_CHAT_LATEST_PAGE });
	if (!readBack(page)) {
		return { events: await readForward(client, orgId, sessionId, [], page, 0), startOver: { history: undefined } };
	}
	return {
		events: page.events,
		startOver: {
			history: page.hasOlder
				? { hasOlder: true, nextBefore: page.nextBefore ?? page.events[0]?.sequence ?? CLOUD_CHAT_NEWEST, recentPending: true }
				: undefined,
		},
	};
}

/**
 * One read of a chat's events. With nothing cached it reads the newest page.
 * Otherwise it reads what came after the newest cached event; when more came
 * than one page holds, the rows between are unknown, and loading older pages
 * starts below them, so nothing could fill them: the list starts over at the
 * newest page instead.
 */
export async function readCloudChatEvents(client: ChatEventsClient, orgId: string, sessionId: string, cached: CloudCpClientEvent[] | undefined): Promise<CloudChatRead> {
	if (!cached?.length) return readLatest(client, orgId, sessionId);
	const after = cached.at(-1)!.sequence;
	const page = await client.listChatEvents(orgId, sessionId, { after, before: CLOUD_CHAT_NEWEST, limit: CLOUD_CHAT_WIDE_PAGE });
	if (!readBack(page)) return { events: await readForward(client, orgId, sessionId, cached, page, after) };
	if (page.hasOlder) return readLatest(client, orgId, sessionId);
	return { events: appendNewer(cached, page.events) };
}

/**
 * Reads a chat's events and records what the list holds: the chat's query
 * function. The live stream may have added events while the read ran; they
 * are kept, except below a list that started over (the stream replays from
 * the old list's end, and the rows between are read as older pages).
 */
export async function loadCloudChatHistory(queryClient: QueryClient, client: ChatEventsClient, orgId: string, sessionId: string): Promise<CloudCpClientEvent[]> {
	const eventsKey = cloudChatEventsKey(orgId, sessionId);
	const read = await readCloudChatEvents(client, orgId, sessionId, queryClient.getQueryData<CloudCpClientEvent[]>(eventsKey));
	const current = queryClient.getQueryData<CloudCpClientEvent[]>(eventsKey) ?? [];
	if (!read.startOver) return mergeCloudEvents(current, read.events);
	const historyKey = cloudChatHistoryKey(orgId, sessionId);
	const previous = queryClient.getQueryData<CloudChatHistory>(historyKey);
	const next = read.startOver.history;
	if (next) {
		queryClient.setQueryData<CloudChatHistory>(historyKey, { ...next, loadingOlder: false, generation: (previous?.generation ?? 0) + 1 });
	} else if (previous) {
		// The whole history: no older pages, and none in flight may land.
		queryClient.setQueryData<CloudChatHistory>(historyKey, { hasOlder: false, nextBefore: 0, recentPending: false, loadingOlder: false, generation: previous.generation + 1 });
	}
	return appendNewer(read.events, current);
}

/**
 * Both lists' events in sequence order, each once. The poll and the live stream
 * write the same cached list, so either may already hold events the other is
 * about to add; a union never drops one because the other got further ahead.
 */
export function mergeCloudEvents(existing: CloudCpClientEvent[], incoming: CloudCpClientEvent[]): CloudCpClientEvent[] {
	const lastSequence = existing.at(-1)?.sequence ?? 0;
	if (incoming.every((event, index) => event.sequence > lastSequence && (index === 0 || event.sequence > incoming[index - 1].sequence))) {
		return incoming.length ? [...existing, ...incoming] : existing;
	}
	const bySequence = new Map<number, CloudCpClientEvent>();
	for (const event of existing) bySequence.set(event.sequence, event);
	let added = false;
	for (const event of incoming) {
		if (bySequence.has(event.sequence)) continue;
		bySequence.set(event.sequence, event);
		added = true;
	}
	return added ? [...bySequence.values()].sort((a, b) => a.sequence - b.sequence) : existing;
}

/**
 * Streamed events the list can take: none below its first event while older
 * history remains, where they would leave a hole older pages cannot fill.
 */
export function streamedCloudChatEvents(queryClient: QueryClient, orgId: string, sessionId: string, current: readonly CloudCpClientEvent[], events: CloudCpClientEvent[]): CloudCpClientEvent[] {
	const first = current[0]?.sequence;
	if (first === undefined || !cloudChatHistoryPartial(queryClient, orgId, sessionId)) return events;
	return events.filter((event) => event.sequence >= first);
}

/**
 * Reads the page before the list's first event and adds it in front: the half
 * hour before the first page (`recent`), or the next older page, which the
 * chat reads as the reader scrolls up. One read at a time; a page read for a
 * list that has since started over is dropped.
 */
export async function loadOlderCloudChatEvents(
	queryClient: QueryClient,
	client: ChatEventsClient,
	orgId: string,
	sessionId: string,
	{ recent = false }: { recent?: boolean } = {},
): Promise<void> {
	const eventsKey = cloudChatEventsKey(orgId, sessionId);
	const historyKey = cloudChatHistoryKey(orgId, sessionId);
	const history = queryClient.getQueryData<CloudChatHistory>(historyKey);
	const events = queryClient.getQueryData<CloudCpClientEvent[]>(eventsKey);
	if (!history || !events || history.loadingOlder) return;
	if (!history.hasOlder) {
		if (history.recentPending) queryClient.setQueryData<CloudChatHistory>(historyKey, { ...history, recentPending: false });
		return;
	}
	const generation = history.generation;
	const oldestAt = Date.parse(events[0]?.createdAt ?? "");
	const since = recent && Number.isFinite(oldestAt) ? new Date(oldestAt - CLOUD_CHAT_RECENT_MS).toISOString() : undefined;
	queryClient.setQueryData<CloudChatHistory>(historyKey, { ...history, loadingOlder: true, recentPending: recent ? false : history.recentPending });
	const settle = (update: Partial<CloudChatHistory>) => queryClient.setQueryData<CloudChatHistory>(historyKey, (current) =>
		current?.generation === generation ? { ...current, loadingOlder: false, ...update } : current);
	let page: CloudCpChatEventsResponse;
	try {
		page = await client.listChatEvents(orgId, sessionId, { before: history.nextBefore, limit: recent ? CLOUD_CHAT_WIDE_PAGE : CLOUD_CHAT_OLDER_PAGE, since });
	} catch (error) {
		settle({});
		throw error;
	}
	if (queryClient.getQueryData<CloudChatHistory>(historyKey)?.generation !== generation) return;
	if (!readBack(page)) {
		// The control plane no longer reads back (a rollback): read the whole history again, forward.
		settle({ hasOlder: false });
		await queryClient.resetQueries({ queryKey: eventsKey, exact: true });
		return;
	}
	queryClient.setQueryData<CloudCpClientEvent[]>(eventsKey, (current) => {
		if (!current) return current;
		const first = current[0]?.sequence ?? Infinity;
		const older = page.events.filter((event) => event.sequence < first);
		return older.length ? [...older, ...current] : current;
	});
	settle({ hasOlder: page.hasOlder, nextBefore: page.nextBefore ?? page.events[0]?.sequence ?? history.nextBefore });
}
