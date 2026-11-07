import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { subscribeCloudNotificationHints } from "../lib/cloud-notification-hints";
import {
	applyCloudInboxChange,
	cloudAlertKey,
	cloudInboxQueryKey,
	cloudInboxQueryRoot,
	cloudInboxSupport,
	cloudInboxUnavailable,
	cloudNotificationsQueryKey,
	fetchCloudInboxList,
	isLegacyCloudNeedsYou,
	MAX_STATE_CHANGE_IDS,
	publishLiveCloudNotificationEvent,
	subscribeCloudInboxSupport,
} from "../lib/cloud-notifications";
import { CloudCpError } from "../lib/cloud-cp/errors";
import { subscribeMyNotificationEventsBridged, subscribeNotificationEventsBridged } from "../lib/cloud-cp/stream-bridge";
import type { CloudCpClient } from "../lib/cloud-cp/client";
import type { CloudCpNotification, CloudCpNotificationListResponse, CloudCpNotificationStateChange } from "../lib/cloud-cp/types";
import { useCloudCp } from "./useCloudCp";
import { useCloudOrg } from "./useCloudOrg";
import { cloudSessionsQueryKey } from "./useWorkspaceQuery";
import { orchestratorChildrenQueryKey } from "./useOrchestratorChildren";

type UseCloudNotificationsOptions = {
	/**
	 * Open this instance's own live stream. The app's CloudNotificationRuntime
	 * already keeps the lists live, so every other reader passes false rather
	 * than holding a duplicate stream.
	 */
	live?: boolean;
};

/**
 * Which inbox the control plane serves: "inbox" for team notifications (the
 * caller's notifications from every organization, triaged on the server),
 * "legacy" for one from before them (the active organization's list, read when
 * the bell opens, cleared on this device), "pending" until the first answer.
 */
export type CloudNotificationsMode = "pending" | "inbox" | "legacy";

// Reconnect pacing for the live stream: quick after a clean close, backing off
// while the control plane keeps failing so an outage isn't hammered.
const STREAM_RETRY_MS = 1000;
const STREAM_RETRY_MAX_MS = 30_000;
// A Needs you read this fresh is still a safe cursor for a new stream: the
// probe that found the inbox has usually just made it.
const STREAM_CURSOR_MAX_AGE_MS = 5000;

const EMPTY: CloudCpNotification[] = [];

export function useCloudNotifications({ live = false }: UseCloudNotificationsOptions = {}) {
	const { client, ready, baseUrl, userId } = useCloudCp();
	const { org } = useCloudOrg();
	const orgId = org?.id ?? "";
	// Normalize the base URL once so the query key and every invalidation agree.
	// A trailing slash otherwise makes the invalidation key miss the query key,
	// so live updates would only land on mount/focus.
	const base = baseUrl.replace(/\/+$/, "");
	const queryClient = useQueryClient();
	const support = useCloudInboxSupport(base);
	const signedIn = ready && base !== "" && userId !== "";
	const legacyEnabled = signedIn && orgId !== "" && support === "unsupported";
	const mode: CloudNotificationsMode = !signedIn || support === "unknown" ? "pending" : support === "supported" ? "inbox" : "legacy";

	// The Needs you read doubles as the probe that tells the two inboxes apart.
	const needsYouQuery = useQuery({
		queryKey: cloudInboxQueryKey(base, userId, "needs_you"),
		enabled: signedIn && support !== "unsupported",
		retry: retryInboxRead,
		queryFn: () => fetchCloudInboxList(base, (query) => client.listMyNotifications(query), "needs_you"),
	});
	const updatesQuery = useQuery({
		queryKey: cloudInboxQueryKey(base, userId, "updates"),
		enabled: signedIn && support === "supported",
		retry: retryInboxRead,
		queryFn: () => fetchCloudInboxList(base, (query) => client.listMyNotifications(query), "updates"),
	});
	const legacyQuery = useQuery({
		queryKey: cloudNotificationsQueryKey(base, orgId, userId, "all"),
		enabled: legacyEnabled,
		queryFn: async () => client.listNotifications(orgId, { status: "all" }),
	});

	useInboxStream(live && signedIn && support === "supported", client, base, userId, queryClient);
	useLegacyStream(live && legacyEnabled, base, orgId, userId, queryClient);
	const legacy = useLegacyClears(base, orgId, userId, legacyQuery.data?.items);

	const inboxRoot = useMemo(() => cloudInboxQueryRoot(base, userId), [base, userId]);
	const invalidateInbox = useCallback(() => queryClient.invalidateQueries({ queryKey: inboxRoot }), [inboxRoot, queryClient]);
	const invalidateLegacy = useCallback(
		() => queryClient.invalidateQueries({ queryKey: ["cloud-notifications", base, orgId, userId] }),
		[base, orgId, queryClient, userId],
	);

	// One triage action: it shows at once in every cached tab, then each tab is
	// read again so the server's answer settles it (and undoes it on failure).
	const changeState = useCallback(async (change: Omit<CloudCpNotificationStateChange, "ids">, ids: readonly string[]) => {
		const unique = [...new Set(ids)];
		if (mode !== "inbox" || unique.length === 0) return;
		await queryClient.cancelQueries({ queryKey: inboxRoot });
		queryClient.setQueriesData<CloudCpNotificationListResponse>({ queryKey: inboxRoot }, (page) =>
			page ? applyCloudInboxChange(page, { ...change, ids: unique }) : page);
		try {
			for (let start = 0; start < unique.length; start += MAX_STATE_CHANGE_IDS) {
				await client.changeNotificationState({ ...change, ids: unique.slice(start, start + MAX_STATE_CHANGE_IDS) });
			}
		} finally {
			await invalidateInbox();
		}
	}, [client, inboxRoot, invalidateInbox, mode, queryClient]);

	const inboxNeedsYou = needsYouQuery.data?.items;
	const inboxUpdates = updatesQuery.data?.items;
	const legacyItems = legacy.items;
	const lists = useMemo(() => {
		if (mode === "inbox") {
			const needsYou = inboxNeedsYou ?? EMPTY;
			const updates = inboxUpdates ?? EMPTY;
			return { needsYou, updates, all: mergeByNewest(needsYou, updates) };
		}
		if (mode === "legacy") {
			return {
				needsYou: legacyItems.filter(isLegacyCloudNeedsYou),
				updates: legacyItems.filter((row) => !isLegacyCloudNeedsYou(row)),
				all: legacyItems,
			};
		}
		return { needsYou: EMPTY, updates: EMPTY, all: EMPTY };
	}, [inboxNeedsYou, inboxUpdates, legacyItems, mode]);

	const markRead = useCallback(async (ids: readonly string[]) => {
		if (mode === "inbox") return changeState({ read: true }, ids);
		if (mode !== "legacy" || orgId === "" || ids.length === 0) return;
		// Acknowledge concrete ids only. The control plane answers 404 for a row
		// that is already read, which is the goal, so that is not a failure here.
		try {
			await Promise.all(ids.map((id) => client.markNotificationsRead(orgId, [id]).catch(ignoreAlreadyRead)));
		} finally {
			await invalidateLegacy();
		}
	}, [changeState, client, invalidateLegacy, mode, orgId]);
	// Unread, done and snooze exist only on a control plane with team notifications.
	const markUnread = useCallback((ids: readonly string[]) => changeState({ read: false }, ids), [changeState]);
	const setDone = useCallback((ids: readonly string[], done: boolean) => changeState({ done }, ids), [changeState]);
	const snooze = useCallback((ids: readonly string[], until: Date) => changeState({ snoozedUntil: until.toISOString() }, ids), [changeState]);

	const loadedRows = lists.all;
	const markAllRead = useCallback(async () => {
		if (mode === "inbox") return changeState({ read: true }, loadedRows.filter((row) => row.status === "unread").map((row) => row.id));
		if (mode !== "legacy" || orgId === "") return;
		await client.markNotificationsRead(orgId);
		await invalidateLegacy();
	}, [changeState, client, invalidateLegacy, loadedRows, mode, orgId]);

	// Clearing is server-side done on a control plane with team notifications,
	// so it holds on every device. Clear all leaves what still blocks someone (an
	// approval or question nobody has answered): it clears when it is handled,
	// or when the person dismisses that one row. An older control plane has no
	// delete or done: clearing marks rows read there and hides them on this
	// device.
	const legacyClearAll = legacy.clearAll;
	const legacyClearOne = legacy.clearOne;
	const clearAll = useCallback(async () => {
		if (mode === "inbox") return setDone(loadedRows.filter((row) => !isLegacyCloudNeedsYou(row)).map((row) => row.id), true);
		if (mode !== "legacy" || orgId === "") return;
		await legacyClearAll(markAllRead);
	}, [legacyClearAll, loadedRows, markAllRead, mode, orgId, setDone]);
	const clearOne = useCallback(async (notification: CloudCpNotification) => {
		if (mode === "inbox") return setDone([notification.id], true);
		if (mode !== "legacy" || orgId === "") return;
		await legacyClearOne(notification, async () => {
			if (notification.status === "unread") await markRead([notification.id]);
		});
	}, [legacyClearOne, markRead, mode, orgId, setDone]);

	const active = mode === "legacy" ? legacyQuery : needsYouQuery;
	const refetchActive = active.refetch;
	const refetchUpdates = updatesQuery.refetch;
	const refetch = useCallback(async () => {
		await refetchActive();
		if (mode === "inbox") await refetchUpdates();
	}, [mode, refetchActive, refetchUpdates]);
	return {
		mode,
		/** Active rows that block the person, newest first. */
		needsYou: lists.needsYou,
		/** Everything else that is still active, resolved blocking rows included. */
		updates: lists.updates,
		all: lists.all,
		/** Blocking rows: the server's count on an inbox, the loaded rows otherwise. */
		needsYouCount: mode === "inbox" ? needsYouQuery.data?.needsYouCount ?? lists.needsYou.length : lists.needsYou.length,
		unreadCount: (mode === "inbox" ? needsYouQuery.data?.unreadCount : mode === "legacy" ? legacyQuery.data?.unreadCount : 0) ?? 0,
		isLoading: signedIn && active.isLoading,
		isError: active.isError,
		refetch,
		markRead,
		markUnread,
		setDone,
		snooze,
		markAllRead,
		clearAll,
		clearOne,
	};
}

export type CloudNotifications = ReturnType<typeof useCloudNotifications>;

/**
 * Whether the signed-in control plane has team notifications, as the inbox
 * probe learned it: "unknown" until that answer arrives.
 */
export function useCloudInboxSupport(baseUrl: string) {
	return useSyncExternalStore(subscribeCloudInboxSupport, () => cloudInboxSupport(baseUrl));
}

function mergeByNewest(...lists: CloudCpNotification[][]): CloudCpNotification[] {
	const seen = new Set<string>();
	const merged: CloudCpNotification[] = [];
	for (const row of lists.flat()) {
		if (seen.has(row.id)) continue;
		seen.add(row.id);
		merged.push(row);
	}
	return merged.sort((a, b) => Date.parse(b.createdAt) - Date.parse(a.createdAt));
}

function retryInboxRead(failureCount: number, error: unknown): boolean {
	return !cloudInboxUnavailable(error) && failureCount < 3;
}

/**
 * Marks a cloud session's unread rows read while it is the session on screen
 * and the window has focus: opening a session reads what it raised, and
 * anything it raises while you watch. Each row is read once per thing that
 * raised it, so marking it unread again sticks until the session raises it
 * anew. A control plane from before team notifications keeps reading rows
 * when the bell opens instead.
 */
export function useReadCloudSessionNotifications(sessionId: string | undefined, enabled: boolean): void {
	const cloud = useCloudNotifications();
	const attentive = useWindowAttention();
	const handled = useRef<{ sessionId: string | undefined; keys: Set<string> }>({ sessionId: undefined, keys: new Set() });
	const unread = useMemo(() => {
		if (cloud.mode !== "inbox" || !sessionId) return EMPTY;
		return cloud.all.filter((row) => row.sessionId === sessionId && row.status === "unread");
	}, [cloud.all, cloud.mode, sessionId]);
	const markRead = cloud.markRead;
	useEffect(() => {
		if (handled.current.sessionId !== sessionId) handled.current = { sessionId, keys: new Set() };
		if (!enabled || !attentive || unread.length === 0) return;
		const fresh = unread.filter((row) => !handled.current.keys.has(cloudAlertKey(row)));
		if (fresh.length === 0) return;
		const keys = fresh.map(cloudAlertKey);
		const owner = handled.current;
		for (const key of keys) owner.keys.add(key);
		void markRead(fresh.map((row) => row.id)).catch(() => {
			// Try again the next time the session's rows change.
			for (const key of keys) owner.keys.delete(key);
		});
	}, [attentive, enabled, markRead, sessionId, unread]);
}

/** Whether the person can see this window: it is visible and has focus. */
function useWindowAttention(): boolean {
	const [attentive, setAttentive] = useState(windowAttentive);
	useEffect(() => {
		const update = () => setAttentive(windowAttentive());
		window.addEventListener("focus", update);
		window.addEventListener("blur", update);
		document.addEventListener("visibilitychange", update);
		update();
		return () => {
			window.removeEventListener("focus", update);
			window.removeEventListener("blur", update);
			document.removeEventListener("visibilitychange", update);
		};
	}, []);
	return attentive;
}

function windowAttentive(): boolean {
	return document.visibilityState === "visible" && document.hasFocus();
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
 * from the sequence of a fresh read of Needs you, so it carries only events
 * newer than what the lists show: neither the first load nor a reconnect
 * replays old events, and every event it delivers is published for desktop
 * notifications.
 */
function useInboxStream(enabled: boolean, client: CloudCpClient, base: string, userId: string, queryClient: QueryClient): void {
	useEffect(() => {
		if (!enabled) return;
		const controller = new AbortController();
		const root = cloudInboxQueryRoot(base, userId);
		const refresh = () => { void queryClient.invalidateQueries({ queryKey: root }); };
		const stopHints = subscribeCloudNotificationHints(refresh);
		const run = async () => {
			let delay = STREAM_RETRY_MS;
			while (!controller.signal.aborted) {
				let after: number | undefined;
				try {
					const page = await queryClient.fetchQuery({
						queryKey: cloudInboxQueryKey(base, userId, "needs_you"),
						queryFn: () => fetchCloudInboxList(base, (query) => client.listMyNotifications(query), "needs_you"),
						staleTime: STREAM_CURSOR_MAX_AGE_MS,
					});
					after = page.latestSequence;
				} catch {
					// Read again after the pause below.
				}
				if (controller.signal.aborted) return;
				let failed = after === undefined;
				if (after !== undefined) {
					// Updates may have moved while no stream was open.
					void queryClient.invalidateQueries({ queryKey: cloudInboxQueryKey(base, userId, "updates") });
					await subscribeMyNotificationEventsBridged({
						baseUrl: base,
						after,
						signal: controller.signal,
						onEvent: (event) => {
							refresh();
							void queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey });
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

/** The active organization's stream, on a control plane from before team notifications. */
function useLegacyStream(enabled: boolean, base: string, orgId: string, userId: string, queryClient: QueryClient): void {
	useEffect(() => {
		if (!enabled) return;
		const notificationsKey = cloudNotificationsQueryKey(base, orgId, userId, "all");
		const controller = new AbortController();
		// The control plane owns the authoritative list, unread count, and sequence
		// (all paginated/server-computed), so a durable event or a pre-durable hint
		// refreshes from the source rather than being patched into the cache — which
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
	}, [base, enabled, orgId, queryClient, userId]);
}

/**
 * Rows cleared on this device, for a control plane from before team
 * notifications. Hidden per signed-in user: notifications belong to one
 * recipient, and an org can be shared by several accounts on this device.
 */
function useLegacyClears(base: string, orgId: string, userId: string, loaded: CloudCpNotification[] | undefined) {
	const storageKey = clearStorageKey(base, orgId, userId);
	const clears = useSyncExternalStore(subscribeClears, () => readClearsRaw(storageKey));
	const items = useMemo(() => {
		const state = parseClears(clears);
		return (loaded ?? EMPTY).filter((item) => !isCleared(item, state));
	}, [clears, loaded]);

	// A cleared row the control plane re-raises (in place: status back to unread,
	// updated_at bumped) is revived for good, so marking it read later cannot
	// hide it again.
	useEffect(() => {
		const state = parseClears(readClearsRaw(storageKey));
		const revived = (loaded ?? EMPTY).filter((item) => isRevived(item, state));
		if (revived.length === 0) return;
		const ids = { ...state.ids };
		for (const item of revived) ids[item.id] = null;
		writeClears(storageKey, { ...state, ids });
	}, [loaded, storageKey]);

	// The control plane has no delete endpoint, so clearing marks rows read
	// server-side and hides them on this device. Markers are the server's own
	// timestamps, so local clock skew cannot hide a newer notification.
	const clearAll = useCallback(async (markAllRead: () => Promise<void>) => {
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
	}, [loaded, storageKey]);
	const clearOne = useCallback(async (notification: CloudNotificationRow, markRead: () => Promise<void>) => {
		const previous = readClearsRaw(storageKey);
		const state = parseClears(previous);
		writeClears(storageKey, { ...state, ids: { ...state.ids, [notification.id]: timestamp(notification.updatedAt) } });
		try {
			await markRead();
		} catch (error) {
			// Put the row back so the list agrees with the error it reports.
			restoreClears(storageKey, previous);
			throw error;
		}
	}, [storageKey]);
	return { items, clearAll, clearOne };
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
