import { useCallback, useEffect, useMemo, useSyncExternalStore } from "react";
import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { subscribeCloudNotificationHints } from "../lib/cloud-notification-hints";
import {
	cloudInboxQueryKey,
	cloudInboxSupport,
	cloudInboxSupportConfirmed,
	cloudInboxUnavailable,
	cloudNotificationsQueryKey,
	MENTIONED,
	probeCloudInbox,
	publishLiveCloudNotificationEvent,
	subscribeCloudInboxSupport,
} from "../lib/cloud-notifications";
import { CloudCpError } from "../lib/cloud-cp/errors";
import { subscribeMyNotificationEventsBridged, subscribeNotificationEventsBridged } from "../lib/cloud-cp/stream-bridge";
import type { CloudCpClient } from "../lib/cloud-cp/client";
import type { CloudCpNotification, CloudCpNotificationListResponse } from "../lib/cloud-cp/types";
import { useCloudCp } from "./useCloudCp";
import { useCloudOrg } from "./useCloudOrg";
import { cloudSessionsQueryKey, sharedCloudWorkspacesQueryKey } from "./useWorkspaceQuery";
import { orchestratorChildrenQueryKey } from "./useOrchestratorChildren";

type UseCloudNotificationsOptions = {
	/**
	 * Open this instance's own live stream. The shell's CloudNotificationRuntime
	 * already keeps the list live, so readers of that list pass false rather
	 * than holding a duplicate stream.
	 */
	live?: boolean;
};

/**
 * Which list the bell shows: "inbox" is the caller's notifications from every
 * organization (/me/notifications, so a mentioned guest gets theirs too);
 * "legacy" is the active organization's list, on a control plane from before
 * mentions (its /me route answers 404, 405 or 501); "pending" until the first
 * answer.
 */
export type CloudNotificationsMode = "pending" | "inbox" | "legacy";

// Reconnect pacing for the live stream: quick after a clean close, backing off
// while the control plane keeps failing so an outage isn't hammered.
const STREAM_RETRY_MS = 1000;
const STREAM_RETRY_MAX_MS = 30_000;
// An inbox read this fresh is still a safe cursor for a new stream: the probe
// that found the inbox has usually just made it.
const STREAM_CURSOR_MAX_AGE_MS = 5000;
/** At most this many ids go in one state change (the contract's cap). */
const MAX_STATE_CHANGE_IDS = 200;

const EMPTY: CloudCpNotification[] = [];

export function useCloudNotifications(
	status: "all" | "unread" | "read" = "all",
	{ live = true }: UseCloudNotificationsOptions = {},
) {
	const { client, ready, baseUrl, userId } = useCloudCp();
	const { org } = useCloudOrg();
	const orgId = org?.id ?? "";
	// Normalize the base URL once so the query key and every invalidation agree.
	// A trailing slash otherwise makes the invalidation key miss the query key,
	// so live updates would only land on mount/focus.
	const base = baseUrl.replace(/\/+$/, "");
	const queryClient = useQueryClient();
	const support = useCloudInboxSupport(base);
	const confirmed = useSyncExternalStore(subscribeCloudInboxSupport, () => cloudInboxSupportConfirmed(base));
	const signedIn = ready && base !== "" && userId !== "";
	const mode: CloudNotificationsMode = !signedIn ? "pending" : support === "supported" ? "inbox" : support === "unsupported" ? "legacy" : "pending";

	// The inbox read doubles as the probe that tells the two lists apart. A
	// remembered "unsupported" still asks once this run: the control plane may
	// have been upgraded since.
	const inboxKey = cloudInboxQueryKey(base, userId);
	const inboxQuery = useQuery({
		queryKey: inboxKey,
		enabled: signedIn && (support !== "unsupported" || !confirmed),
		retry: retryInboxRead,
		queryFn: () => probeCloudInbox(base, () => client.listMyNotifications({ status: "all" })),
	});
	const legacyKey = cloudNotificationsQueryKey(base, orgId, userId, status);
	const legacyQuery = useQuery({
		queryKey: legacyKey,
		enabled: signedIn && orgId !== "" && mode === "legacy",
		queryFn: async () => client.listNotifications(orgId, { status }),
	});

	useInboxStream(live && mode === "inbox", client, base, userId, queryClient);
	useLegacyStream(live && mode === "legacy" && orgId !== "", base, orgId, userId, status, queryClient);

	const active = mode === "legacy" ? legacyQuery : inboxQuery;
	// Cleared rows are hidden per signed-in user: notifications belong to one
	// recipient, and an org can be shared by several accounts on this device.
	// The inbox has no delete either, so its rows clear the same way, under "me".
	const storageKey = clearStorageKey(base, mode === "legacy" ? orgId : "me", userId);
	const loaded = mode === "pending" ? undefined : active.data?.items;
	const clears = useSyncExternalStore(subscribeClears, () => readClearsRaw(storageKey));
	const items = useMemo(() => {
		const state = parseClears(clears);
		return (loaded ?? EMPTY).filter((item) => !isCleared(item, state));
	}, [clears, loaded]);

	// A cleared row the control plane re-raises (in place: status back to unread,
	// updated_at bumped, as a repeated mention does) is revived for good, so
	// marking it read later cannot hide it again.
	useEffect(() => {
		const state = parseClears(readClearsRaw(storageKey));
		const revived = (loaded ?? EMPTY).filter((item) => isRevived(item, state));
		if (revived.length === 0) return;
		const ids = { ...state.ids };
		for (const item of revived) ids[item.id] = null;
		writeClears(storageKey, { ...state, ids });
	}, [loaded, storageKey]);

	const invalidateAll = useCallback(async () => {
		if (mode === "inbox") await queryClient.invalidateQueries({ queryKey: cloudInboxQueryKey(base, userId) });
		else await queryClient.invalidateQueries({ queryKey: ["cloud-notifications", base, orgId, userId] });
	}, [base, mode, orgId, queryClient, userId]);
	const markAllRead = useCallback(async () => {
		if (mode === "inbox") {
			await client.changeMyNotificationState({ all: true, read: true });
			await invalidateAll();
			return;
		}
		if (mode !== "legacy" || orgId === "") return;
		await client.markNotificationsRead(orgId);
		await invalidateAll();
	}, [client, invalidateAll, mode, orgId]);
	// Acknowledge concrete ids only. On the org list the control plane answers
	// 404 for a row that is already read, which is the goal, so that is not a
	// failure here; the inbox changes nothing for such an id.
	const markRead = useCallback(async (notificationIds: string[]) => {
		const ids = [...new Set(notificationIds)];
		if (ids.length === 0) return;
		if (mode === "inbox") {
			markReadInCache(queryClient, inboxKey, (row) => ids.includes(row.id));
			try {
				for (let start = 0; start < ids.length; start += MAX_STATE_CHANGE_IDS) {
					await client.changeMyNotificationState({ ids: ids.slice(start, start + MAX_STATE_CHANGE_IDS), read: true });
				}
			} finally {
				await invalidateAll();
			}
			return;
		}
		if (mode !== "legacy" || orgId === "") return;
		try {
			await Promise.all(ids.map((id) => client.markNotificationsRead(orgId, [id]).catch(ignoreAlreadyRead)));
		} finally {
			await invalidateAll();
		}
	}, [client, inboxKey, invalidateAll, mode, orgId, queryClient]);
	// There is no delete: clearing marks rows read on the control plane and
	// hides them on this device. Markers are the server's own timestamps, so
	// local clock skew cannot hide a newer notification.
	const clearAll = useCallback(async () => {
		if (mode === "pending" || (mode === "legacy" && orgId === "")) return;
		const rows = loaded ?? EMPTY;
		const previous = readClearsRaw(storageKey);
		const state = parseClears(previous);
		const newest = rows.reduce((max, item) => Math.max(max, timestamp(item.createdAt)), state.before);
		const ids: CloudClears["ids"] = {};
		for (const [id, marker] of Object.entries(state.ids)) if (marker !== null) ids[id] = marker;
		for (const item of rows) ids[item.id] = timestamp(item.updatedAt);
		writeClears(storageKey, { before: newest, ids });
		try {
			await markAllRead();
		} catch (error) {
			restoreClears(storageKey, previous);
			throw error;
		}
	}, [loaded, markAllRead, mode, orgId, storageKey]);
	const clearOne = useCallback(async (notification: CloudNotificationRow) => {
		if (mode === "pending" || (mode === "legacy" && orgId === "")) return;
		const previous = readClearsRaw(storageKey);
		const state = parseClears(previous);
		writeClears(storageKey, { ...state, ids: { ...state.ids, [notification.id]: timestamp(notification.updatedAt) } });
		try {
			if (notification.status === "unread") await markRead([notification.id]);
		} catch (error) {
			// Put the row back so the list agrees with the error it reports.
			restoreClears(storageKey, previous);
			throw error;
		}
	}, [markRead, mode, orgId, storageKey]);
	return {
		...active,
		data: mode === "pending" ? undefined : active.data,
		isLoading: signedIn && (mode === "pending" ? inboxQuery.isLoading : active.isLoading),
		mode,
		items,
		markAllRead,
		markRead,
		clearAll,
		clearOne,
	};
}

export type CloudNotifications = ReturnType<typeof useCloudNotifications>;

/**
 * Whether the signed-in control plane has mentions and the personal inbox, as
 * the inbox probe learned it (or the last run did): "unknown" until then.
 */
export function useCloudInboxSupport(baseUrl: string) {
	return useSyncExternalStore(subscribeCloudInboxSupport, () => cloudInboxSupport(baseUrl));
}

/**
 * Reads the mentions a session raised for this person (opening the session
 * reads them). The rows read at once in the cache, so no badge shows while
 * the person is looking; the inbox is read again after the control plane
 * answers.
 */
export async function readCloudSessionMentions(
	queryClient: QueryClient,
	client: Pick<CloudCpClient, "changeMyNotificationState">,
	baseUrl: string,
	userId: string,
	orgId: string,
	sessionId: string,
): Promise<void> {
	const key = cloudInboxQueryKey(baseUrl, userId);
	const unread = (row: CloudCpNotification) => row.sessionId === sessionId && row.orgId === orgId && row.type === MENTIONED && row.status === "unread";
	if (!orgId || !sessionId || !(queryClient.getQueryData<CloudCpNotificationListResponse>(key)?.items ?? []).some(unread)) return;
	markReadInCache(queryClient, key, unread);
	try {
		await client.changeMyNotificationState({ orgId, sessionId, read: true });
	} finally {
		await queryClient.invalidateQueries({ queryKey: key });
	}
}

function retryInboxRead(failureCount: number, error: unknown): boolean {
	return !cloudInboxUnavailable(error) && failureCount < 3;
}

function markReadInCache(queryClient: QueryClient, key: readonly unknown[], match: (row: CloudCpNotification) => boolean): void {
	queryClient.setQueryData<CloudCpNotificationListResponse>(key, (page) => {
		if (!page) return page;
		let read = 0;
		const items = page.items.map((row) => {
			if (row.status !== "unread" || !match(row)) return row;
			read += 1;
			return { ...row, status: "read" as const };
		});
		return read === 0 ? page : { ...page, items, unreadCount: Math.max(0, page.unreadCount - read) };
	});
}

/** Resolves after `ms`, or at once when the signal aborts. */
function pause(ms: number, signal: AbortSignal): Promise<void> {
	return new Promise<void>((resolve) => {
		const onAbort = () => { clearTimeout(timer); resolve(); };
		const timer = setTimeout(() => {
			signal.removeEventListener("abort", onAbort);
			resolve();
		}, ms);
		signal.addEventListener("abort", onAbort, { once: true });
	});
}

/**
 * Keeps the inbox live from the caller's /me stream. Each connection resumes
 * from the sequence of a fresh inbox read, so it carries only events newer
 * than what the list shows: neither the first load nor a reconnect replays
 * old events, and every event it delivers is published for desktop alerts and
 * for prefetching the session a mention points at.
 */
function useInboxStream(enabled: boolean, client: CloudCpClient, base: string, userId: string, queryClient: QueryClient): void {
	useEffect(() => {
		if (!enabled) return;
		const controller = new AbortController();
		const key = cloudInboxQueryKey(base, userId);
		// The control plane owns the authoritative list, unread count, and
		// sequence, so a durable event or a pre-durable hint refreshes from the
		// source rather than being patched into the cache.
		const refresh = () => { void queryClient.invalidateQueries({ queryKey: key }); };
		const stopHints = subscribeCloudNotificationHints(refresh);
		const run = async () => {
			let delay = STREAM_RETRY_MS;
			while (!controller.signal.aborted) {
				let after: number | undefined;
				try {
					const page = await queryClient.fetchQuery({
						queryKey: key,
						queryFn: () => probeCloudInbox(base, () => client.listMyNotifications({ status: "all" })),
						staleTime: STREAM_CURSOR_MAX_AGE_MS,
					});
					after = page.latestSequence;
				} catch {
					// Read again after the pause below.
				}
				if (controller.signal.aborted) return;
				let failed = after === undefined;
				if (after !== undefined) {
					await subscribeMyNotificationEventsBridged({
						baseUrl: base,
						after,
						signal: controller.signal,
						onEvent: (event) => {
							refresh();
							void queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey });
							void queryClient.invalidateQueries({ queryKey: sharedCloudWorkspacesQueryKey });
							void queryClient.invalidateQueries({ queryKey: orchestratorChildrenQueryKey });
							publishLiveCloudNotificationEvent(event);
						},
						onError: () => { failed = true; },
					});
				}
				if (controller.signal.aborted) return;
				refresh();
				delay = failed ? Math.min(delay * 2, STREAM_RETRY_MAX_MS) : STREAM_RETRY_MS;
				await pause(delay, controller.signal);
			}
		};
		void run();
		return () => { stopHints(); controller.abort(); };
	}, [base, client, enabled, queryClient, userId]);
}

/** The active organization's stream, on a control plane from before mentions. */
function useLegacyStream(enabled: boolean, base: string, orgId: string, userId: string, status: "all" | "unread" | "read", queryClient: QueryClient): void {
	useEffect(() => {
		if (!enabled) return;
		const notificationsKey = cloudNotificationsQueryKey(base, orgId, userId, status);
		const controller = new AbortController();
		// The control plane owns the authoritative list, unread count, and sequence
		// (all paginated/server-computed), so a durable event or a pre-durable hint
		// refreshes from the source rather than being patched into the cache, which
		// would drift the unread badge and can't render a hint that carries no
		// title/body. A hint arrives over the terminal WebSocket before the durable
		// inbox row commits, so it acts as a low-latency refetch trigger; REST/SSE
		// remain the recovery source after a reconnect.
		const refresh = () => { void queryClient.invalidateQueries({ queryKey: notificationsKey }); };
		const stopHints = subscribeCloudNotificationHints(refresh);
		let after: number | undefined;
		const reconnect = async () => {
			while (!controller.signal.aborted) {
				await subscribeNotificationEventsBridged({ baseUrl: base, orgId, after, signal: controller.signal, onEvent: (event) => {
					after = Math.max(after ?? 0, event.sequence);
					refresh();
					void queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey });
					void queryClient.invalidateQueries({ queryKey: orchestratorChildrenQueryKey });
				}, onError: refresh });
				if (controller.signal.aborted) return;
				refresh();
				await pause(STREAM_RETRY_MS, controller.signal);
			}
		};
		void reconnect();
		return () => { stopHints(); controller.abort(); };
	}, [base, enabled, orgId, queryClient, status, userId]);
}

/**
 * `before` hides every row created at or before it (clear all). `ids` holds
 * per-row markers: the row's server `updatedAt` when it was cleared, or null
 * for a row that was re-raised after a clear and must stay visible.
 */
export type CloudClears = { before: number; ids: Record<string, number | null> };
type CloudNotificationRow = { id: string; createdAt: string; updatedAt: string; status: "unread" | "read" };

// Keep the most recent per-row markers; older rows fall under the clear-all
// cutoff anyway. Capped so the stored map cannot grow without bound.
const MAX_CLEARED_IDS = 200;
const clearListeners = new Set<() => void>();
const clearStorageKey = (base: string, orgId: string, userId: string) =>
	`ao.cloudNotifications.cleared:${base}:${orgId}:${userId}`;

const timestamp = (value: string) => Date.parse(value) || 0;

function clearMarker(item: CloudNotificationRow, clears: CloudClears): number | null | undefined {
	if (item.id in clears.ids) return clears.ids[item.id];
	return timestamp(item.createdAt) <= clears.before ? clears.before : undefined;
}

function isRevived(item: CloudNotificationRow, clears: CloudClears): boolean {
	const marker = clearMarker(item, clears);
	return typeof marker === "number" && item.status === "unread" && timestamp(item.updatedAt) > marker;
}

export function isCleared(item: CloudNotificationRow, clears: CloudClears): boolean {
	const marker = clearMarker(item, clears);
	if (marker === undefined || marker === null) return false;
	return !isRevived(item, clears);
}

function ignoreAlreadyRead(error: unknown) {
	if (error instanceof CloudCpError && error.status === 404) return;
	throw error;
}

function subscribeClears(listener: () => void) {
	clearListeners.add(listener);
	return () => { clearListeners.delete(listener); };
}

function readClearsRaw(storageKey: string): string {
	try {
		return window.localStorage.getItem(storageKey) ?? "";
	} catch {
		return "";
	}
}

function parseClears(raw: string): CloudClears {
	try {
		const parsed = JSON.parse(raw) as Partial<CloudClears>;
		return {
			before: typeof parsed.before === "number" ? parsed.before : 0,
			ids: parsed.ids && typeof parsed.ids === "object" ? parsed.ids : {},
		};
	} catch {
		return { before: 0, ids: {} };
	}
}

function writeClears(storageKey: string, clears: CloudClears) {
	const rank = (marker: number | null) => (marker === null ? Number.POSITIVE_INFINITY : marker);
	const ids = Object.entries(clears.ids)
		.sort(([, a], [, b]) => rank(b) - rank(a))
		.slice(0, MAX_CLEARED_IDS);
	restoreClears(storageKey, JSON.stringify({ before: clears.before, ids: Object.fromEntries(ids) }));
}

function restoreClears(storageKey: string, raw: string) {
	try {
		if (raw === "") window.localStorage.removeItem(storageKey);
		else window.localStorage.setItem(storageKey, raw);
	} catch {
		// Storage unavailable: rows stay visible but are still marked read.
	}
	for (const listener of clearListeners) listener();
}
