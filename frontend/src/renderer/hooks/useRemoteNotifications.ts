import { apiErrorException } from "../lib/api-error-exception";
import { useQueries, type QueryClient } from "@tanstack/react-query";
import { useSyncExternalStore } from "react";
import { aoBridge } from "../lib/bridge";
import { baseUrlForHost, clientForHost, connectedHosts, labelForHost, subscribeConnectedHosts } from "../lib/host-clients";
import { probeRemoteSse } from "../lib/remote-sse-probe";
import { useConnectedHosts } from "./useHostConnection";
import { refKey } from "../lib/hosts";
import {
	isResolvableNotificationType,
	NOTIFICATION_PAGE_SIZE,
	type NotificationDTO,
	type NotificationListStatus,
	type NotificationsPage,
} from "../lib/notifications";

export const remoteNotificationsQueryKey = (hostId: string, status: NotificationListStatus) =>
	["remote-notifications", hostId, status] as const;

export const REMOTE_NOTIFICATION_PREFIX = "remote-notification:";

/**
 * The OS toast id for a remote host's notification. Host-qualified, so a
 * dismissal for one machine never closes another machine's toast that happens
 * to share a daemon-local id.
 */
export function remoteNativeNotificationId(hostId: string, id: string): string {
	return `${REMOTE_NOTIFICATION_PREFIX}${refKey({ host: hostId, id })}`;
}

// Remote OS toasts raised for a resolvable notification that its host has not
// resolved yet, by host. Deliberately uncapped and kept across disconnects:
// forgetting a live id would leave its banner up after a missed resolution.
// It shrinks on resolution, delete, and clear.
const liveRemoteToasts = new Map<string, Set<string>>();
const remoteHistoryReconciles = new Map<string, { again: boolean; run: Promise<void> }>();

/** Records a remote toast so a later resolution, live or missed, can withdraw it. */
export function trackRemoteToast(hostId: string, notification: Pick<NotificationDTO, "id" | "type" | "resolvedAt">): void {
	if (notification.resolvedAt || !isResolvableNotificationType(notification.type)) return;
	let ids = liveRemoteToasts.get(hostId);
	if (!ids) {
		ids = new Set();
		liveRemoteToasts.set(hostId, ids);
	}
	ids.add(notification.id);
}

/** Withdraws exactly this host's toast for this id. Read state is untouched. */
function dismissRemoteToast(hostId: string, id: string): void {
	liveRemoteToasts.get(hostId)?.delete(id);
	void aoBridge.notifications.dismiss(remoteNativeNotificationId(hostId, id));
}

/**
 * Pages the host's history for its live toasts. Only a row that reads resolved
 * dismisses; a row missing from history or a failed fetch proves nothing, so
 * the banner stays until a later reconnect or snapshot settles it.
 */
export function reconcileRemoteToastsFromHistory(hostId: string): Promise<void> {
	const inflight = remoteHistoryReconciles.get(hostId);
	if (inflight) {
		inflight.again = true;
		return inflight.run;
	}
	// Nothing tracked: nothing to look up, and nothing to cache as in flight.
	if (!liveRemoteToasts.get(hostId)?.size) return Promise.resolve();
	const state = { again: false, run: Promise.resolve() };
	// Start on a microtask so the in-flight entry is registered before the
	// body can reach `finally`; otherwise a body that finishes synchronously
	// would leave a settled promise cached and block every later lookup.
	state.run = Promise.resolve().then(async () => {
		try {
			do {
				state.again = false;
				const live = liveRemoteToasts.get(hostId);
				const pending = new Set(live);
				const visited = new Set<string>();
				let cursor = "";
				while (pending.size > 0 && !visited.has(cursor)) {
					visited.add(cursor);
					const page = await fetchRemoteNotificationsPage(hostId, "all", cursor);
					for (const item of page.notifications) {
						if (!pending.delete(item.id) || !item.resolvedAt || !live?.has(item.id)) continue;
						dismissRemoteToast(hostId, item.id);
					}
					if (!page.nextCursor) break;
					cursor = page.nextCursor;
				}
			} while (state.again);
		} catch {
			// Unknown is not resolved: keep the banner.
		} finally {
			if (remoteHistoryReconciles.get(hostId) === state) remoteHistoryReconciles.delete(hostId);
		}
	});
	remoteHistoryReconciles.set(hostId, state);
	return state.run;
}

/**
 * Settles live toasts against a fresh host snapshot (first page). A row that
 * reads resolved dismisses its toast; a live id the page does not show (older,
 * or already read off the unread list) is looked up in the full history.
 */
export function reconcileRemoteToastsFromSnapshot(hostId: string, notifications: readonly NotificationDTO[]): void {
	const live = liveRemoteToasts.get(hostId);
	if (!live?.size) return;
	const accounted = new Set<string>();
	for (const item of notifications) {
		if (!live.has(item.id)) continue;
		if (item.resolvedAt) dismissRemoteToast(hostId, item.id);
		else accounted.add(item.id);
	}
	if ([...live].some((id) => !accounted.has(id))) void reconcileRemoteToastsFromHistory(hostId);
}

/**
 * Applies a host's live notification frame to its toasts: an authoritative
 * resolution withdraws that exact toast; a deleted or cleared row can no
 * longer resolve, so it stops being tracked (its banner is left alone).
 */
function applyRemoteToastEvent(hostId: string, event: Event): void {
	if (event.type === "notification_cleared") {
		liveRemoteToasts.delete(hostId);
		return;
	}
	if (event.type !== "notification_resolved" && event.type !== "notification_deleted") return;
	const data = (event as MessageEvent<unknown>).data;
	if (typeof data !== "string" || !data) return;
	try {
		const decoded = JSON.parse(data) as { id?: unknown; resolvedAt?: unknown };
		if (typeof decoded.id !== "string" || !decoded.id) return;
		if (event.type === "notification_deleted") {
			liveRemoteToasts.get(hostId)?.delete(decoded.id);
			return;
		}
		if (typeof decoded.resolvedAt !== "string" || !decoded.resolvedAt) return;
		dismissRemoteToast(hostId, decoded.id);
	} catch {
		// A malformed frame still triggers the refetch below.
	}
}

let streamingHosts: string[] = [];
const streamListeners = new Set<() => void>();
const subscribeStreams = (listener: () => void) => {
	streamListeners.add(listener);
	return () => streamListeners.delete(listener);
};
const getStreamingHosts = () => streamingHosts;
function setStreaming(hostId: string, streaming: boolean) {
	if (streamingHosts.includes(hostId) === streaming) return;
	streamingHosts = streaming ? [...streamingHosts, hostId] : streamingHosts.filter((id) => id !== hostId);
	for (const listener of streamListeners) listener();
}

/** One stream per connected host; the existing REST query polls until a frame arrives. */
export function connectRemoteNotificationStreams(queryClient: QueryClient): () => void {
	const connections = new Map<string, { base: string; close: () => void }>();
	const invalidate = (hostId: string) => {
		void queryClient.invalidateQueries({ queryKey: remoteNotificationsQueryKey(hostId, "unread") });
		void queryClient.invalidateQueries({ queryKey: remoteNotificationsQueryKey(hostId, "all") });
	};
	const sync = () => {
		const active = new Set(connectedHosts());
		for (const [hostId, connection] of connections) {
			if (active.has(hostId) && baseUrlForHost(hostId) === connection.base) continue;
			connection.close();
			connections.delete(hostId);
			setStreaming(hostId, false);
		}
		for (const hostId of active) {
			const base = baseUrlForHost(hostId);
			if (!base || connections.has(hostId)) continue;
			const close = probeRemoteSse(
				`${base.replace(/\/+$/, "")}/api/v1/notifications/stream`,
				["notification_created", "notification_resolved", "notification_deleted", "notification_cleared"],
				(event) => { applyRemoteToastEvent(hostId, event); invalidate(hostId); },
				// (Re)connected or fell back to polling: resolutions published
				// while this host was unreachable never arrived as events.
				() => { setStreaming(hostId, true); invalidate(hostId); void reconcileRemoteToastsFromHistory(hostId); },
				() => { setStreaming(hostId, false); invalidate(hostId); void reconcileRemoteToastsFromHistory(hostId); },
			);
			connections.set(hostId, { base, close });
		}
	};
	const unsubscribe = subscribeConnectedHosts(sync);
	sync();
	return () => {
		unsubscribe();
		for (const [hostId, connection] of connections) {
			connection.close();
			setStreaming(hostId, false);
		}
		connections.clear();
	};
}

export async function fetchRemoteNotificationsPage(hostId: string, status: NotificationListStatus, cursor = ""): Promise<NotificationsPage> {
	const { data, error } = await clientForHost(hostId).GET("/api/v1/notifications", {
		params: { query: { status, limit: NOTIFICATION_PAGE_SIZE, cursor: cursor || undefined } },
	});
	if (error || !data) throw apiErrorException(error, "Could not load notifications");
	return data;
}

export async function markRemoteNotificationsRead(hostId: string, ids: string[], queryClient: QueryClient): Promise<void> {
	const { error } = await clientForHost(hostId).POST("/api/v1/notifications/read-all", { body: { ids } });
	if (error) throw apiErrorException(error, "Could not mark notifications read");
	await queryClient.invalidateQueries({ queryKey: remoteNotificationsQueryKey(hostId, "unread") });
	await queryClient.invalidateQueries({ queryKey: remoteNotificationsQueryKey(hostId, "all") });
}

export async function clearRemoteNotifications(hostId: string, queryClient: QueryClient): Promise<void> {
	const { error } = await clientForHost(hostId).DELETE("/api/v1/notifications");
	if (error) throw apiErrorException(error);
	await queryClient.invalidateQueries({ queryKey: ["remote-notifications", hostId] });
}

export async function clearRemoteNotification(hostId: string, id: string, queryClient: QueryClient): Promise<void> {
	const { error, response } = await clientForHost(hostId).DELETE("/api/v1/notifications/{id}", {
		params: { path: { id } },
	});
	if (error && response.status !== 404) throw apiErrorException(error);
	await queryClient.invalidateQueries({ queryKey: ["remote-notifications", hostId] });
}

export function useRemoteNotificationHosts(status: NotificationListStatus, enabled = true) {
	const connected = useConnectedHosts();
	const streaming = useSyncExternalStore(subscribeStreams, getStreamingHosts, getStreamingHosts);
	const queries = useQueries({
		queries: connected.map((hostId) => ({
			queryKey: remoteNotificationsQueryKey(hostId, status),
			queryFn: () => fetchRemoteNotificationsPage(hostId, status),
			enabled,
			retry: 1,
			refetchInterval: streaming.includes(hostId) ? false : 2_000,
		})),
	});
	return {
		hosts: connected.map((hostId, index) => ({
			hostId,
			label: labelForHost(hostId) ?? hostId,
			data: queries[index]?.isError ? undefined : queries[index]?.data,
			isError: queries[index]?.isError ?? false,
			isLoading: queries[index]?.isLoading ?? false,
		})),
		totalUnreadCount: status === "unread"
			? queries.reduce((count, query) => count + (query.isError ? 0 : query.data?.unreadCount ?? 0), 0)
			: 0,
	};
}
