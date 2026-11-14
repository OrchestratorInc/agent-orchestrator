import { apiErrorException } from "./api-error-exception";
import type { InfiniteData, QueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { aoBridge } from "./bridge";
import { apiClient, eventStreamBaseUrl, getApiBaseUrl, subscribeApiBaseUrl } from "./api-client";
import { computeSseRetryDelayMs } from "./sse-backoff";

export type NotificationDTO = components["schemas"]["NotificationResponse"];
export type NotificationsPage = components["schemas"]["ListNotificationsResponse"];
export type NotificationsCache = InfiniteData<NotificationsPage>;
export type NotificationListStatus = "unread" | "all";
export type ClearNotificationsResult = components["schemas"]["ClearNotificationsResponse"];
export type NotificationClear = Pick<ClearNotificationsResult, "clearId" | "clearEpoch" | "clearSequence">;

export const unreadNotificationsQueryKey = ["notifications", "history", "unread"] as const;
export const recentNotificationsQueryKey = ["notifications", "history", "all"] as const;
export const NOTIFICATION_PAGE_SIZE = 100;

const EVENTSOURCE_CLOSED = 2;
const MAX_CONFIRMED_NOTIFICATION_DELETIONS = 256;

const UNRESOLVABLE_TYPES = new Set(["needs_input", "ready_to_merge"]);

type NotificationsQueryKey = typeof unreadNotificationsQueryKey | typeof recentNotificationsQueryKey;

type LiveNotificationEvent =
	| { kind: "created"; notification: NotificationDTO; watched: boolean }
	| { kind: "resolved"; notification: NotificationDTO }
	| { kind: "deleted"; notification: NotificationDTO }
	| { kind: "cleared"; clear: NotificationClear };

const latestClearGeneration = new WeakMap<QueryClient, { epoch: string; sequence: number }>();
const clearedNotificationSnapshots = new WeakMap<QueryClient, NotificationsCache>();
const notificationReconcilers = new WeakMap<QueryClient, () => Promise<void>>();

export function reconcileNotifications(queryClient: QueryClient): Promise<void> {
	const reconcile = notificationReconcilers.get(queryClient);
	if (reconcile) return reconcile();
	return Promise.all([
		queryClient.invalidateQueries({ queryKey: unreadNotificationsQueryKey }),
		queryClient.invalidateQueries({ queryKey: recentNotificationsQueryKey }),
	]).then(() => undefined);
}

type NotificationDeletionState =
	| {
			kind: "optimistic";
			notification: NotificationDTO;
			present: { recent: boolean; unread: boolean };
			positions: { recent?: number; unread?: number };
	  }
	| { kind: "confirmed" };
const notificationDeletionStates = new WeakMap<QueryClient, Map<string, NotificationDeletionState>>();

export function notificationsQueryKey(status: NotificationListStatus): NotificationsQueryKey {
	return status === "unread" ? unreadNotificationsQueryKey : recentNotificationsQueryKey;
}

function isUnresolved(notification: NotificationDTO): boolean {
	return UNRESOLVABLE_TYPES.has(notification.type) && !notification.resolvedAt;
}

export async function fetchNotificationsPage(
	status: NotificationListStatus,
	cursor = "",
	signal?: AbortSignal,
): Promise<NotificationsPage> {
	const { data, error } = await apiClient.GET("/api/v1/notifications", {
		signal,
		params: {
			query: {
				status,
				limit: NOTIFICATION_PAGE_SIZE,
				cursor: cursor || undefined,
			},
		},
	});
	if (error) throw apiErrorException(error, "Could not load notifications");
	const notifications = sortNotifications(data?.notifications ?? []);
	return {
		notifications,
		nextCursor: data?.nextCursor,
		unreadCount: data?.unreadCount ?? notifications.filter((item) => item.status === "unread").length,
		unresolvedCount: data?.unresolvedCount ?? notifications.filter(isUnresolved).length,
	};
}

export async function markAllNotificationsRead(ids: string[]): Promise<number> {
	const { data, error } = await apiClient.POST("/api/v1/notifications/read-all", {
		body: ids.length === 0 ? {} : { ids },
	});
	if (error) throw apiErrorException(error, "Could not mark notifications read");
	return data?.updatedCount ?? 0;
}

export async function clearAllNotifications(): Promise<ClearNotificationsResult> {
	const { data, error } = await apiClient.DELETE("/api/v1/notifications");
	if (error || !data) throw apiErrorException(error, "Could not clear notifications");
	return data;
}

export async function deleteNotification(notification: NotificationDTO): Promise<NotificationDTO> {
	const { data, error, response } = await apiClient.DELETE("/api/v1/notifications/{id}", {
		params: { path: { id: notification.id } },
	});
	if (response.status === 404) return notification;
	if (error || !data) throw apiErrorException(error, "Could not clear notification");
	return data.notification;
}

export function mergeUnreadNotification(queryClient: QueryClient, notification: NotificationDTO): boolean {
	if (notification.status !== "unread") return false;
	const inserted = mergeNotificationIntoCache(queryClient, unreadNotificationsQueryKey, notification);
	rebaseOversizedFirstPage(queryClient, unreadNotificationsQueryKey);
	return inserted;
}

function mergeRecentNotification(queryClient: QueryClient, notification: NotificationDTO): boolean {
	const inserted = mergeNotificationIntoCache(queryClient, recentNotificationsQueryKey, notification);
	rebaseOversizedFirstPage(queryClient, recentNotificationsQueryKey);
	return inserted;
}

export function applyResolvedNotification(queryClient: QueryClient, notification: NotificationDTO): boolean {
	let foundInEveryCache = true;
	for (const queryKey of [unreadNotificationsQueryKey, recentNotificationsQueryKey] as const) {
		let found = false;
		queryClient.setQueryData<NotificationsCache>(queryKey, (current) => {
			if (!current) return current;
			const existing = getCachedNotifications(current).find((item) => item.id === notification.id);
			if (!existing) return current;
			found = true;
			const unresolvedDelta = Number(isUnresolved(notification)) - Number(isUnresolved(existing));
			return {
				...current,
				pages: current.pages.map((page) => ({
					...page,
					notifications: page.notifications.map((item) => (item.id === notification.id ? notification : item)),
					unresolvedCount: Math.max(0, page.unresolvedCount + unresolvedDelta),
				})),
			};
		});
		if (!found) foundInEveryCache = false;
	}
	return foundInEveryCache;
}

function deletionStates(queryClient: QueryClient): Map<string, NotificationDeletionState> {
	let states = notificationDeletionStates.get(queryClient);
	if (!states) {
		states = new Map();
		notificationDeletionStates.set(queryClient, states);
	}
	return states;
}

function pruneConfirmedNotificationDeletions(states: Map<string, NotificationDeletionState>): void {
	let confirmedCount = 0;
	for (const state of states.values()) {
		if (state.kind === "confirmed") confirmedCount++;
	}
	if (confirmedCount <= MAX_CONFIRMED_NOTIFICATION_DELETIONS) return;
	for (const [id, state] of states) {
		if (state.kind !== "confirmed") continue;
		states.delete(id);
		confirmedCount--;
		if (confirmedCount <= MAX_CONFIRMED_NOTIFICATION_DELETIONS) return;
	}
}

function cachedPageIndex(
	queryClient: QueryClient,
	queryKey: NotificationsQueryKey,
	id: string,
): number | undefined {
	return queryClient
		.getQueryData<NotificationsCache>(queryKey)
		?.pages.findIndex((page) => page.notifications.some((item) => item.id === id));
}

function removeNotificationFromCaches(
	queryClient: QueryClient,
	notification: NotificationDTO,
	requirePresent = false,
): void {
	for (const queryKey of [unreadNotificationsQueryKey, recentNotificationsQueryKey] as const) {
		queryClient.setQueryData<NotificationsCache>(queryKey, (current) => {
			if (!current) return current;
			const present = getCachedNotifications(current).some((item) => item.id === notification.id);
			if (requirePresent && !present) return current;
			return {
				...current,
				pages: current.pages.map((page) => ({
					...page,
					notifications: page.notifications.filter((item) => item.id !== notification.id),
					unreadCount: Math.max(0, page.unreadCount - Number(notification.status === "unread")),
					unresolvedCount: Math.max(0, page.unresolvedCount - Number(isUnresolved(notification))),
				})),
			};
		});
	}
}

export function applyOptimisticNotificationDelete(queryClient: QueryClient, notification: NotificationDTO): void {
	const states = deletionStates(queryClient);
	if (states.has(notification.id)) return;
	const unreadIndex = cachedPageIndex(queryClient, unreadNotificationsQueryKey, notification.id);
	const recentIndex = cachedPageIndex(queryClient, recentNotificationsQueryKey, notification.id);
	states.set(notification.id, {
		kind: "optimistic",
		notification,
		present: {
			unread: unreadIndex !== undefined && unreadIndex >= 0,
			recent: recentIndex !== undefined && recentIndex >= 0,
		},
		positions: {
			unread: unreadIndex === -1 ? undefined : unreadIndex,
			recent: recentIndex === -1 ? undefined : recentIndex,
		},
	});
	removeNotificationFromCaches(queryClient, notification, true);
}

export function applyNotificationDeleted(queryClient: QueryClient, notification: NotificationDTO): boolean {
	const states = deletionStates(queryClient);
	const current = states.get(notification.id);
	if (current?.kind === "confirmed") return false;
	states.delete(notification.id);
	states.set(notification.id, { kind: "confirmed" });
	pruneConfirmedNotificationDeletions(states);
	if (current?.kind === "optimistic") return false;
	removeNotificationFromCaches(queryClient, notification);
	return true;
}

export function rollbackOptimisticNotificationDelete(queryClient: QueryClient, id: string): boolean {
	const states = deletionStates(queryClient);
	const state = states.get(id);
	if (state?.kind !== "optimistic") return false;
	states.delete(id);
	for (const [name, queryKey] of [
		["unread", unreadNotificationsQueryKey],
		["recent", recentNotificationsQueryKey],
	] as const) {
		if (!state.present[name]) continue;
		queryClient.setQueryData<NotificationsCache>(queryKey, (current) => {
			if (!current) return current;
			const pageIndex = state.positions[name];
			const alreadyPresent = getCachedNotifications(current).some((item) => item.id === id);
			if (pageIndex === undefined || pageIndex >= current.pages.length || alreadyPresent) return current;
			const pages = current.pages.map((page, index) => ({
				...page,
				notifications:
					pageIndex === index
						? sortNotifications([...page.notifications, state.notification])
						: page.notifications,
				unreadCount: page.unreadCount + Number(state.notification.status === "unread"),
				unresolvedCount: page.unresolvedCount + Number(isUnresolved(state.notification)),
			}));
			return { ...current, pages };
		});
	}
	return true;
}

function mergeNotificationIntoCache(
	queryClient: QueryClient,
	queryKey: NotificationsQueryKey,
	notification: NotificationDTO,
): boolean {
	let inserted = false;
	queryClient.setQueryData<NotificationsCache>(queryKey, (current) => {
		if (!current || current.pages.length === 0) {
			inserted = true;
			return {
				pageParams: [""],
				pages: [
					{
						notifications: [notification],
						unreadCount: notification.status === "unread" ? 1 : 0,
						unresolvedCount: isUnresolved(notification) ? 1 : 0,
					},
				],
			};
		}

		const existing = getCachedNotifications(current).find((item) => item.id === notification.id);
		const unreadDelta = (notification.status === "unread" ? 1 : 0) - (existing?.status === "unread" ? 1 : 0);
		const unresolvedDelta =
			(isUnresolved(notification) ? 1 : 0) - (existing && isUnresolved(existing) ? 1 : 0);
		const pages = current.pages.map((page) => ({
			...page,
			notifications: page.notifications.map((item) => (item.id === notification.id ? notification : item)),
			unreadCount: Math.max(0, page.unreadCount + unreadDelta),
			unresolvedCount: Math.max(0, page.unresolvedCount + unresolvedDelta),
		}));

		if (existing) {
			return { ...current, pages };
		}

		inserted = true;
		pages[0] = {
			...pages[0],
			notifications: sortNotifications([notification, ...pages[0].notifications]),
		};
		return { ...current, pages };
	});
	return inserted;
}

export function markAllCachedNotificationsRead(
	queryClient: QueryClient,
	ids: string[],
	updatedCount?: number,
): void {
	if (ids.length === 0) {
		queryClient.setQueryData<NotificationsCache>(unreadNotificationsQueryKey, (current) => {
			if (!current) {
				return { pageParams: [""], pages: [{ notifications: [], unreadCount: 0, unresolvedCount: 0 }] };
			}
			return {
				pageParams: [""],
				pages: [
					{
						notifications: [],
						unreadCount: 0,
						unresolvedCount: current.pages[0]?.unresolvedCount ?? 0,
					},
				],
			};
		});
		queryClient.setQueryData<NotificationsCache>(recentNotificationsQueryKey, (current) => {
			if (!current) return current;
			return {
				...current,
				pages: current.pages.map((page) => ({
					...page,
					notifications: page.notifications.map((item) =>
						item.status === "read" ? item : { ...item, status: "read" as const },
					),
					unreadCount: 0,
				})),
			};
		});
		return;
	}

	const acknowledged = new Set(ids);
	for (const queryKey of [unreadNotificationsQueryKey, recentNotificationsQueryKey] as const) {
		queryClient.setQueryData<NotificationsCache>(queryKey, (current) => {
			if (!current) return current;

			let clearedAcrossPages = 0;
			const pages = current.pages.map((page) => {
				const notifications = page.notifications.map((item) => {
					if (!acknowledged.has(item.id) || item.status === "read") return item;
					clearedAcrossPages++;
					return { ...item, status: "read" as const };
				});
				return { ...page, notifications };
			});
			const delta = updatedCount ?? clearedAcrossPages;

			return {
				...current,
				pages: pages.map((page) => ({
					...page,
					unreadCount: Math.max(0, page.unreadCount - delta),
				})),
			};
		});
	}
}

export function applyNotificationsCleared(queryClient: QueryClient, clear: NotificationClear): boolean {
	const latest = latestClearGeneration.get(queryClient);
	if (latest?.epoch === clear.clearEpoch && latest.sequence >= clear.clearSequence) return false;
	latestClearGeneration.set(queryClient, { epoch: clear.clearEpoch, sequence: clear.clearSequence });
	const states = deletionStates(queryClient);
	for (const id of states.keys()) {
		states.set(id, { kind: "confirmed" });
	}
	pruneConfirmedNotificationDeletions(states);
	for (const queryKey of [unreadNotificationsQueryKey, recentNotificationsQueryKey] as const) {
		queryClient.setQueryData<NotificationsCache>(queryKey, {
			pageParams: [""],
			pages: [{ notifications: [], unreadCount: 0, unresolvedCount: 0 }],
		});
	}
	const recentSnapshot = queryClient.getQueryData<NotificationsCache>(recentNotificationsQueryKey);
	if (recentSnapshot) clearedNotificationSnapshots.set(queryClient, recentSnapshot);
	return true;
}

export function isNotificationsCacheFromClear(queryClient: QueryClient): boolean {
	const cleared = clearedNotificationSnapshots.get(queryClient);
	return Boolean(cleared) && queryClient.getQueryData(recentNotificationsQueryKey) === cleared;
}

export function getCachedNotifications(cache: NotificationsCache | undefined): NotificationDTO[] {
	if (!cache) return [];
	const byID = new Map<string, NotificationDTO>();
	for (const page of cache.pages) {
		for (const notification of page.notifications) {
			if (!byID.has(notification.id)) byID.set(notification.id, notification);
		}
	}
	return sortNotifications([...byID.values()]);
}

export function getCachedUnreadCount(cache: NotificationsCache | undefined): number {
	return (
		cache?.pages[0]?.unreadCount ?? getCachedNotifications(cache).filter((item) => item.status === "unread").length
	);
}

export function keepLatestNotificationsPage(
	queryClient: QueryClient,
	queryKey: NotificationsQueryKey = unreadNotificationsQueryKey,
): void {
	queryClient.setQueryData<NotificationsCache>(queryKey, (current) => {
		if (!current || current.pages.length <= 1) return current;
		return {
			pages: [current.pages[0]],
			pageParams: [current.pageParams[0]],
		};
	});
	rebaseOversizedFirstPage(queryClient, queryKey);
}

function isWatchingNeedsInputSession(
	notification: NotificationDTO,
	visibleAgentSessionId: string | undefined,
): boolean {
	if (notification.type !== "needs_input") return false;
	if (!notification.sessionId || notification.sessionId !== visibleAgentSessionId) return false;
	return document.visibilityState === "visible" && document.hasFocus();
}

export function createNotificationsTransport(
	queryClient: QueryClient,
	getVisibleAgentSessionId: () => string | undefined = () => undefined,
) {
	return {
		connect() {
			let retryTimer: ReturnType<typeof setTimeout> | undefined;
			let source: EventSource | undefined;
			let sourceBaseUrl: string | undefined;
			let snapshotRefresh:
				| {
						dirty: boolean;
						events: LiveNotificationEvent[];
						done: Promise<void>;
						resolve: () => void;
				  }
				| undefined;
			let pendingLiveEvents: Promise<void> | undefined;

			const applyLiveNotificationEvent = (event: LiveNotificationEvent): Promise<void> | void => {
				if (event.kind === "cleared") {
					return queryClient
						.cancelQueries({ queryKey: ["notifications", "history"] }, { revert: false })
						.catch(() => undefined)
						.then(() => {
							applyNotificationsCleared(queryClient, event.clear);
						});
				}
				if (event.kind === "deleted") {
					return queryClient
						.cancelQueries({ queryKey: ["notifications", "history"] }, { revert: false })
						.catch(() => undefined)
						.then(() => {
							applyNotificationDeleted(queryClient, event.notification);
						});
				}
				if (event.kind === "resolved") {
					if (!applyResolvedNotification(queryClient, event.notification)) {
						void invalidateNotifications();
					}
					return;
				}
				const inserted = mergeUnreadNotification(queryClient, event.notification);
				mergeRecentNotification(queryClient, event.notification);
				if (inserted) {
					void aoBridge.notifications.show({
						id: event.notification.id,
						title: event.notification.title,
						body: event.notification.body || undefined,
						type: event.notification.type,
						watched: event.watched,
					});
				}
			};

			const enqueueLiveNotificationEvent = (event: LiveNotificationEvent): Promise<void> | undefined => {
				if (!pendingLiveEvents && event.kind !== "cleared" && event.kind !== "deleted") {
					applyLiveNotificationEvent(event);
					return undefined;
				}
				const applied = (pendingLiveEvents ?? Promise.resolve()).then(
					() => applyLiveNotificationEvent(event),
					() => applyLiveNotificationEvent(event),
				);
				const settled = applied.then(
					() => undefined,
					() => undefined,
				);
				pendingLiveEvents = settled;
				void settled.then(() => {
					if (pendingLiveEvents === settled) pendingLiveEvents = undefined;
				});
				return settled;
			};

			const receiveLiveNotificationEvent = (event: LiveNotificationEvent) => {
				if (snapshotRefresh) {
					snapshotRefresh.events.push(event);
					if (event.kind === "cleared" || event.kind === "deleted") snapshotRefresh.dirty = true;
					return;
				}
				enqueueLiveNotificationEvent(event);
			};

			const invalidateNotifications = (): Promise<void> => {
				if (snapshotRefresh) {
					snapshotRefresh.dirty = true;
					return snapshotRefresh.done;
				}
				let resolveRefresh!: () => void;
				const done = new Promise<void>((resolve) => {
					resolveRefresh = resolve;
				});
				const refresh = {
					dirty: false,
					events: [] as LiveNotificationEvent[],
					done,
					resolve: resolveRefresh,
				};
				snapshotRefresh = refresh;
				const finish = async () => {
					try {
						if (snapshotRefresh !== refresh) return;
						snapshotRefresh = undefined;
						for (const event of refresh.events) enqueueLiveNotificationEvent(event);
						await pendingLiveEvents;
						if (refresh.dirty) await invalidateNotifications();
					} finally {
						refresh.resolve();
					}
				};
				void Promise.all([
					queryClient.invalidateQueries({ queryKey: unreadNotificationsQueryKey }),
					queryClient.invalidateQueries({ queryKey: recentNotificationsQueryKey }),
				]).then(finish, finish);
				return refresh.done;
			};
			notificationReconcilers.set(queryClient, invalidateNotifications);

			let retries = 0;

			const scheduleRetry = () => {
				if (retryTimer) return;
				retries += 1;
				retryTimer = setTimeout(() => {
					retryTimer = undefined;
					connectSource();
				}, computeSseRetryDelayMs(retries));
			};

			const connectSource = () => {
				if (typeof EventSource === "undefined") return;
				const baseUrl = getApiBaseUrl();
				if (source && sourceBaseUrl === baseUrl && source.readyState !== EVENTSOURCE_CLOSED) return;
				if (sourceBaseUrl && sourceBaseUrl !== baseUrl) retries = 0;
				source?.close();
				source = undefined;
				sourceBaseUrl = baseUrl;
				try {
					source = new EventSource(`${eventStreamBaseUrl(baseUrl)}/api/v1/notifications/stream`);
					source.onopen = () => {
						retries = 0;
						invalidateNotifications();
					};
					source.onerror = () => {
						if (source?.readyState === EVENTSOURCE_CLOSED) scheduleRetry();
					};
					source.addEventListener("notification_created", (event) => {
						const notification = parseNotificationEvent(event);
						if (!notification) return;
						receiveLiveNotificationEvent({
							kind: "created",
							notification,
							watched: isWatchingNeedsInputSession(notification, getVisibleAgentSessionId()),
						});
					});
					source.addEventListener("notification_resolved", (event) => {
						const notification = parseNotificationEvent(event);
						if (!notification) return;
						receiveLiveNotificationEvent({ kind: "resolved", notification });
					});
					source.addEventListener("notification_deleted", (event) => {
						const notification = parseNotificationEvent(event);
						if (!notification) return;
						receiveLiveNotificationEvent({ kind: "deleted", notification });
					});
					source.addEventListener("notification_cleared", (event) => {
						const clear = parseNotificationClearEvent(event);
						if (clear) receiveLiveNotificationEvent({ kind: "cleared", clear });
					});
				} catch {
					source = undefined;
				}
			};

			const removeDaemonListener = aoBridge.daemon.onStatus(() => {
				connectSource();
				invalidateNotifications();
			});
			const removeBaseUrlListener = subscribeApiBaseUrl(() => {
				connectSource();
				invalidateNotifications();
			});
			connectSource();

			return () => {
				if (retryTimer) clearTimeout(retryTimer);
				if (notificationReconcilers.get(queryClient) === invalidateNotifications) {
					notificationReconcilers.delete(queryClient);
				}
				removeDaemonListener();
				removeBaseUrlListener();
				source?.close();
			};
		},
	};
}

function parseNotificationClearEvent(event: Event): NotificationClear | null {
	const data = (event as MessageEvent<string>).data;
	if (typeof data !== "string" || data === "") return null;
	try {
		const decoded = JSON.parse(data) as {
			clearId?: unknown;
			clearEpoch?: unknown;
			clearSequence?: unknown;
		};
		if (
			typeof decoded.clearId !== "string" ||
			!decoded.clearId ||
			typeof decoded.clearEpoch !== "string" ||
			!decoded.clearEpoch ||
			typeof decoded.clearSequence !== "number" ||
			!Number.isSafeInteger(decoded.clearSequence) ||
			decoded.clearSequence <= 0
		) {
			return null;
		}
		return {
			clearId: decoded.clearId,
			clearEpoch: decoded.clearEpoch,
			clearSequence: decoded.clearSequence,
		};
	} catch {
		return null;
	}
}

function parseNotificationEvent(event: Event): NotificationDTO | null {
	const data = (event as MessageEvent<string>).data;
	if (typeof data !== "string" || data === "") return null;
	try {
		return JSON.parse(data) as NotificationDTO;
	} catch {
		return null;
	}
}

function sortNotifications(notifications: NotificationDTO[]): NotificationDTO[] {
	return [...notifications].sort((a, b) => {
		const byTime = Date.parse(b.createdAt) - Date.parse(a.createdAt);
		return byTime || b.id.localeCompare(a.id);
	});
}

function rebaseOversizedFirstPage(queryClient: QueryClient, queryKey: NotificationsQueryKey): void {
	const cache = queryClient.getQueryData<NotificationsCache>(queryKey);
	if (!cache || cache.pages[0]?.notifications.length <= NOTIFICATION_PAGE_SIZE) return;
	const query = queryClient.getQueryCache().find({ queryKey, exact: true });
	if (!query?.isActive()) {
		queryClient.setQueryData<NotificationsCache>(queryKey, (current) => {
			if (!current?.pages[0]) return current;
			return {
				...current,
				pages: [
					{
						...current.pages[0],
						notifications: current.pages[0].notifications.slice(0, NOTIFICATION_PAGE_SIZE),
					},
					...current.pages.slice(1),
				],
			};
		});
	}
	void queryClient.invalidateQueries({ queryKey, exact: true, refetchType: "active" });
}
