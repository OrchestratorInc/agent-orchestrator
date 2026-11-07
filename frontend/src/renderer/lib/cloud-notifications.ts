import { CloudCpError } from "./cloud-cp/errors";
import type {
	CloudCpNotification,
	CloudCpNotificationEvent,
	CloudCpNotificationListQuery,
	CloudCpNotificationListResponse,
	CloudCpNotificationStateChange,
} from "./cloud-cp/types";

// The cloud notification list is server-authoritative (paginated items, unread
// count, and event sequence all computed by the control plane), so the renderer
// keys a single react-query cache off the notification endpoint and refreshes it
// from durable events and pre-durable hints rather than reconstructing that state
// client-side. The base URL is normalized here so a trailing slash can never make
// an invalidation key miss the query key.
export const cloudNotificationsQueryKey = (baseUrl: string, orgId: string, userId: string, status: "all" | "unread" | "read" = "all") =>
	["cloud-notifications", baseUrl.replace(/\/+$/, ""), orgId, userId, status] as const;

/**
 * The caller's inbox on a control plane with team notifications: one cache
 * per tab, across every organization. "me" sits where an org id would, and an
 * org id is a UUID, so the two key families never meet.
 */
export type CloudInboxList = "needs_you" | "updates";

export const cloudInboxQueryRoot = (baseUrl: string, userId: string) =>
	["cloud-notifications", baseUrl.replace(/\/+$/, ""), "me", userId] as const;

export const cloudInboxQueryKey = (baseUrl: string, userId: string, list: CloudInboxList) =>
	[...cloudInboxQueryRoot(baseUrl, userId), list] as const;

/** The server-side filter behind each inbox list: active rows of that tab. */
export const CLOUD_INBOX_LIST_QUERY: Record<CloudInboxList, CloudCpNotificationListQuery> = {
	needs_you: { view: "needs_you", state: "active" },
	updates: { view: "updates", state: "active" },
};

/** At most this many ids go in one state change. */
export const MAX_STATE_CHANGE_IDS = 200;

/**
 * Whether the control plane predates team notifications: it has no /me inbox
 * route, so its router answers with a bare 404 (no error code), or it has the
 * route without the store behind it and answers 501. A 404 with a code is about
 * something that was asked for, not about the route.
 */
export function cloudInboxUnavailable(error: unknown): boolean {
	if (!(error instanceof CloudCpError)) return false;
	return (error.status === 404 && !error.code) || error.status === 405 || error.status === 501;
}

export type CloudInboxSupport = "unknown" | "supported" | "unsupported";

// Learned once per control plane from the first /me inbox answer and kept for
// the app run: every hook, menu and settings page asks the same server, so they
// agree on whether team notifications exist there.
const inboxSupport = new Map<string, CloudInboxSupport>();
const supportListeners = new Set<() => void>();

const supportKey = (baseUrl: string) => baseUrl.replace(/\/+$/, "");

export function cloudInboxSupport(baseUrl: string): CloudInboxSupport {
	return inboxSupport.get(supportKey(baseUrl)) ?? "unknown";
}

export function recordCloudInboxSupport(baseUrl: string, support: Exclude<CloudInboxSupport, "unknown">): void {
	const key = supportKey(baseUrl);
	if (key === "" || inboxSupport.get(key) === support) return;
	inboxSupport.set(key, support);
	for (const listener of supportListeners) listener();
}

export function subscribeCloudInboxSupport(listener: () => void): () => void {
	supportListeners.add(listener);
	return () => { supportListeners.delete(listener); };
}

/** Test seam: forget what was learned about every control plane. */
export function resetCloudInboxSupport(): void {
	inboxSupport.clear();
	for (const listener of supportListeners) listener();
}

/**
 * Lists one inbox tab and records what the answer says about the control
 * plane: any answer means team notifications exist; a missing route means
 * they don't. Other failures (offline, 5xx) say nothing and stay errors.
 */
export async function fetchCloudInboxList(
	baseUrl: string,
	list: (query: CloudCpNotificationListQuery) => Promise<CloudCpNotificationListResponse>,
	which: CloudInboxList,
): Promise<CloudCpNotificationListResponse> {
	try {
		const page = await list(CLOUD_INBOX_LIST_QUERY[which]);
		recordCloudInboxSupport(baseUrl, "supported");
		return page;
	} catch (error) {
		if (cloudInboxUnavailable(error)) recordCloudInboxSupport(baseUrl, "unsupported");
		throw error;
	}
}

/**
 * Whether a row belongs in Needs you when the control plane can't say (it
 * predates team notifications, so rows carry no tier): a question that has
 * not been answered yet, as the local inbox treats one.
 */
export function isLegacyCloudNeedsYou(row: Pick<CloudCpNotification, "type" | "resolvedAt" | "tier">): boolean {
	if (row.tier !== undefined) return row.tier === 0 && !row.resolvedAt;
	return row.type === "needs_input" && !row.resolvedAt;
}

/** When a snooze ends: in an hour, or at 9:00 tomorrow, local time. */
export type CloudSnoozeChoice = "hour" | "tomorrow";

export function cloudSnoozeUntil(choice: CloudSnoozeChoice, now: Date = new Date()): Date {
	if (choice === "hour") return new Date(now.getTime() + 60 * 60 * 1000);
	return new Date(now.getFullYear(), now.getMonth(), now.getDate() + 1, 9, 0, 0, 0);
}

/**
 * Applies a triage change to a cached inbox page the way the server will, so
 * a row leaves (done, snoozed) or changes read state as soon as it is asked
 * for. The refetch that follows replaces it with the server's own answer.
 */
export function applyCloudInboxChange(
	page: CloudCpNotificationListResponse,
	change: CloudCpNotificationStateChange,
): CloudCpNotificationListResponse {
	const ids = new Set(change.ids);
	const leaves = change.done === true || (change.snoozedUntil !== undefined && change.snoozedUntil !== "");
	let unreadCount = page.unreadCount;
	let needsYouCount = page.needsYouCount;
	const items: CloudCpNotification[] = [];
	for (const row of page.items) {
		if (!ids.has(row.id)) {
			items.push(row);
			continue;
		}
		const status = change.read === undefined ? row.status : change.read ? "read" : "unread";
		if (status !== row.status) unreadCount = Math.max(0, unreadCount + (status === "read" ? -1 : 1));
		if (leaves) {
			// Done and snoozed rows stop blocking; read state alone never changes Needs you.
			if (needsYouCount !== undefined && row.tier === 0 && !row.resolvedAt) needsYouCount = Math.max(0, needsYouCount - 1);
			continue;
		}
		items.push(status === row.status ? row : { ...row, status });
	}
	return { ...page, items, unreadCount, ...(needsYouCount === undefined ? {} : { needsYouCount }) };
}

/**
 * Whether a live event deserves a desktop notification: the recipient asked
 * for it on the desktop, and it still waits on them (unread, unresolved, not
 * done, not snoozed). Rows from control planes before team notifications carry
 * no desktop flag, so they never qualify.
 */
export function shouldAlertCloudNotification(event: CloudCpNotificationEvent, now: number = Date.now()): boolean {
	if (event.kind !== "notification_created" && event.kind !== "notification_updated") return false;
	const row = event.notification;
	if (row.desktop !== true || row.status !== "unread") return false;
	if (row.resolvedAt || row.doneAt) return false;
	if (row.snoozedUntil && Date.parse(row.snoozedUntil) > now) return false;
	return true;
}

/**
 * One desktop notification per row per thing that raised it. Marking a row
 * unread elsewhere changes its state but not its source event, so it never
 * notifies twice; an approval that moves on to the next request does.
 */
export function cloudAlertKey(row: Pick<CloudCpNotification, "id" | "eventId" | "createdAt">): string {
	return `${row.id}:${row.eventId ?? row.createdAt}`;
}

type LiveEventListener = (event: CloudCpNotificationEvent) => void;
const liveEventListeners = new Set<LiveEventListener>();

/**
 * Events from a /me stream opened after the inbox was read, so every one is
 * new to this app run: a replay of what the list already showed is never
 * published (see useCloudNotifications).
 */
export function publishLiveCloudNotificationEvent(event: CloudCpNotificationEvent): void {
	for (const listener of liveEventListeners) listener(event);
}

export function subscribeLiveCloudNotificationEvents(listener: LiveEventListener): () => void {
	liveEventListeners.add(listener);
	return () => { liveEventListeners.delete(listener); };
}
