import type { QueryClient } from "@tanstack/react-query";
import type { CloudCpClient, CloudCpClientEvent, CloudCpChatEventsResponse } from "./cloud-cp";

export const CLOUD_CHAT_NEWEST = 9_007_199_254_740_991;
export const CLOUD_CHAT_LATEST_PAGE = 150;
export const CLOUD_CHAT_RECENT_MS = 30 * 60_000;
export const CLOUD_CHAT_OLDER_PAGE = 200;
const CLOUD_CHAT_WIDE_PAGE = 500;

type ChatEventsClient = Pick<CloudCpClient, "listChatEvents">;

export interface CloudChatHistory {
	hasOlder: boolean;
	nextBefore: number;
	recentPending: boolean;
	loadingOlder: boolean;
	generation: number;
}

export function cloudChatEventsKey(orgId: string, sessionId: string) {
	return ["cloud-chat-events", orgId, sessionId] as const;
}

export function cloudChatHistoryKey(orgId: string, sessionId: string) {
	return ["cloud-chat-history", orgId, sessionId] as const;
}

export function clearCloudChatCaches(queryClient: QueryClient): void {
	for (const key of ["cloud-chat-events", "cloud-chat-history", "cloud-script-steps"]) queryClient.removeQueries({ queryKey: [key] });
}

export function cloudChatHistoryPartial(queryClient: QueryClient, orgId: string, sessionId: string): boolean {
	return Boolean(queryClient.getQueryData<CloudChatHistory>(cloudChatHistoryKey(orgId, sessionId))?.hasOlder);
}

function readBack(page: CloudCpChatEventsResponse): page is CloudCpChatEventsResponse & { hasOlder: boolean } {
	return typeof page.hasOlder === "boolean";
}

function appendNewer(events: CloudCpClientEvent[], incoming: readonly CloudCpClientEvent[]): CloudCpClientEvent[] {
	const newest = events.at(-1)?.sequence ?? 0;
	const newer = incoming.filter((event) => event.sequence > newest);
	return newer.length ? [...events, ...newer] : events;
}

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

export interface CloudChatRead {
	events: CloudCpClientEvent[];
	startOver?: { history: Omit<CloudChatHistory, "generation" | "loadingOlder"> | undefined };
}

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

export async function readCloudChatEvents(client: ChatEventsClient, orgId: string, sessionId: string, cached: CloudCpClientEvent[] | undefined): Promise<CloudChatRead> {
	if (!cached?.length) return readLatest(client, orgId, sessionId);
	const after = cached.at(-1)!.sequence;
	const page = await client.listChatEvents(orgId, sessionId, { after, before: CLOUD_CHAT_NEWEST, limit: CLOUD_CHAT_WIDE_PAGE });
	if (!readBack(page)) return { events: await readForward(client, orgId, sessionId, cached, page, after) };
	if (page.hasOlder) return readLatest(client, orgId, sessionId);
	return { events: appendNewer(cached, page.events) };
}

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
		queryClient.setQueryData<CloudChatHistory>(historyKey, { hasOlder: false, nextBefore: 0, recentPending: false, loadingOlder: false, generation: previous.generation + 1 });
	}
	return appendNewer(read.events, current);
}

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

export function streamedCloudChatEvents(queryClient: QueryClient, orgId: string, sessionId: string, current: readonly CloudCpClientEvent[], events: CloudCpClientEvent[]): CloudCpClientEvent[] {
	const first = current[0]?.sequence;
	if (first === undefined || !cloudChatHistoryPartial(queryClient, orgId, sessionId)) return events;
	return events.filter((event) => event.sequence >= first);
}

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
