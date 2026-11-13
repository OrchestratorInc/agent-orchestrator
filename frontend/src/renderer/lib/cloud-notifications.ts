import { CloudCpError } from "./cloud-cp/errors";
import type {
	CloudCpAsideReplyNotificationMetadata,
	CloudCpMentionNotificationMetadata,
	CloudCpNotification,
	CloudCpNotificationEvent,
	CloudCpNotificationPreferences,
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
 * The caller's inbox across every organization (/me/notifications). "me" sits
 * where an org id would, and an org id is a UUID, so the two key families
 * never meet.
 */
export const cloudInboxQueryKey = (baseUrl: string, userId: string) =>
	cloudNotificationsQueryKey(baseUrl, "me", userId, "all");

/** A mention notification: someone named the recipient, or a team they are in. */
export const MENTIONED = "mentioned";
/** Someone replied in an aside the recipient is in: one row per aside, replaced by a newer reply. */
export const ASIDE_REPLY = "aside_reply";

/**
 * Whether the control plane predates mentions and the personal inbox: it has
 * no /me route, so its router answers with a bare 404 (no error code), 405,
 * or 501 for a route without its store. A 404 with a code is about something
 * that was asked for, not about the route.
 */
export function cloudInboxUnavailable(error: unknown): boolean {
	if (!(error instanceof CloudCpError)) return false;
	return (error.status === 404 && !error.code) || error.status === 405 || error.status === 501;
}

export type CloudInboxSupport = "unknown" | "supported" | "unsupported";

// Learned once per control plane and app run from the first /me inbox answer,
// so the bell, the composer's @ menu and Settings agree on whether mentions
// exist there. The last answer is remembered on this device, so the next run
// draws them at once (no pop-in) while the probe confirms it.
const STORAGE_PREFIX = "ao.cloudInboxSupport:";
const inboxSupport = new Map<string, Exclude<CloudInboxSupport, "unknown">>();
const supportListeners = new Set<() => void>();

const supportKey = (baseUrl: string) => baseUrl.replace(/\/+$/, "");

function storage(): Storage | undefined {
	try {
		return typeof window === "undefined" ? undefined : window.localStorage;
	} catch {
		return undefined;
	}
}

function rememberedSupport(key: string): Exclude<CloudInboxSupport, "unknown"> | undefined {
	try {
		const value = storage()?.getItem(STORAGE_PREFIX + key);
		return value === "supported" || value === "unsupported" ? value : undefined;
	} catch {
		return undefined;
	}
}

/** What this run learned about a control plane, else what the last run did. */
export function cloudInboxSupport(baseUrl: string): CloudInboxSupport {
	const key = supportKey(baseUrl);
	if (key === "") return "unknown";
	return inboxSupport.get(key) ?? rememberedSupport(key) ?? "unknown";
}

/** Whether this run has its own answer yet (a remembered one is still asked again). */
export function cloudInboxSupportConfirmed(baseUrl: string): boolean {
	return inboxSupport.has(supportKey(baseUrl));
}

export function recordCloudInboxSupport(baseUrl: string, support: Exclude<CloudInboxSupport, "unknown">): void {
	const key = supportKey(baseUrl);
	if (key === "" || inboxSupport.get(key) === support) return;
	inboxSupport.set(key, support);
	try {
		storage()?.setItem(STORAGE_PREFIX + key, support);
	} catch {
		// Without it the next run asks before drawing, as a first run does.
	}
	for (const listener of supportListeners) listener();
}

export function subscribeCloudInboxSupport(listener: () => void): () => void {
	supportListeners.add(listener);
	return () => { supportListeners.delete(listener); };
}

/** Test seam: forget what was learned about every control plane, here and on this device. */
export function resetCloudInboxSupport(): void {
	inboxSupport.clear();
	const saved = storage();
	try {
		const keys = Array.from({ length: saved?.length ?? 0 }, (_, index) => saved?.key(index));
		for (const key of keys) if (key?.startsWith(STORAGE_PREFIX)) saved?.removeItem(key);
	} catch {
		// Nothing remembered to forget.
	}
	for (const listener of supportListeners) listener();
}

/**
 * Runs a request to one of the /me or mention routes and records what its
 * answer says about the control plane: any answer means it has them; a
 * missing route means it doesn't. Other failures (offline, 5xx) say nothing
 * and stay errors.
 */
export async function probeCloudInbox<T>(baseUrl: string, request: () => Promise<T>): Promise<T> {
	try {
		const answer = await request();
		recordCloudInboxSupport(baseUrl, "supported");
		return answer;
	} catch (error) {
		if (cloudInboxUnavailable(error)) recordCloudInboxSupport(baseUrl, "unsupported");
		throw error;
	}
}

/** A mention notification's metadata, read defensively: a field of the wrong type is left out. */
export function mentionMetadata(row: Pick<CloudCpNotification, "type" | "metadata">): CloudCpMentionNotificationMetadata | undefined {
	if (row.type !== MENTIONED) return undefined;
	const raw = row.metadata ?? {};
	const text = (key: string) => (typeof raw[key] === "string" && raw[key] !== "" ? raw[key] as string : undefined);
	const sequence = typeof raw.sequence === "number" && Number.isFinite(raw.sequence) ? raw.sequence : undefined;
	return {
		authorUserId: text("authorUserId"),
		authorName: text("authorName"),
		sequence,
		turnId: text("turnId"),
		teamId: text("teamId"),
		teamName: text("teamName"),
		sessionTitle: text("sessionTitle"),
		projectName: text("projectName"),
		asideId: text("asideId"),
		replyId: text("replyId"),
	};
}

/**
 * Whether a row stays in the bell. A mention or aside reply someone resolved
 * (they wrote in the session, or replied in the aside) is done and leaves it;
 * every other kind stays as before.
 */
export function activeCloudNotification(row: Pick<CloudCpNotification, "type" | "resolvedAt">): boolean {
	return !row.resolvedAt || (row.type !== MENTIONED && row.type !== ASIDE_REPLY);
}

/** An aside reply notification's metadata, read defensively like a mention's. */
export function asideReplyMetadata(row: Pick<CloudCpNotification, "type" | "metadata">): CloudCpAsideReplyNotificationMetadata | undefined {
	if (row.type !== ASIDE_REPLY) return undefined;
	const raw = row.metadata ?? {};
	const text = (key: string) => (typeof raw[key] === "string" && raw[key] !== "" ? raw[key] as string : undefined);
	const sequence = typeof raw.sequence === "number" && Number.isFinite(raw.sequence) ? raw.sequence : undefined;
	return {
		asideId: text("asideId"),
		replyId: text("replyId"),
		authorUserId: text("authorUserId"),
		authorName: text("authorName"),
		sequence,
		turnId: text("turnId"),
		sessionTitle: text("sessionTitle"),
		projectName: text("projectName"),
	};
}

/** The session and project a mention or aside reply row names, for its "session · project" line. */
export function sessionRowMetadata(row: Pick<CloudCpNotification, "type" | "metadata">): { sessionTitle?: string; projectName?: string } | undefined {
	return mentionMetadata(row) ?? asideReplyMetadata(row);
}

/** The preferences a person has before they change any: every alert on. */
export const DEFAULT_NOTIFICATION_PREFERENCES: CloudCpNotificationPreferences = { mentions: true, teamMentions: true, asideReplies: true };

/**
 * Whether a live event deserves a desktop alert: a mention or an aside reply
 * that is new or newly repeated (a newer one replaces the row and arrives as
 * notification_created), still unread and unresolved, and the matching
 * preference is on (`teamMentions` when only a team named them,
 * `asideReplies` for a reply; a control plane that predates that switch
 * leaves it on). The bell lists it either way.
 */
export function shouldAlertCloudNotification(
	event: CloudCpNotificationEvent,
	preferences: CloudCpNotificationPreferences = DEFAULT_NOTIFICATION_PREFERENCES,
): boolean {
	if (event.kind !== "notification_created") return false;
	const row = event.notification;
	if (row.status !== "unread" || row.resolvedAt) return false;
	if (row.type === ASIDE_REPLY) return preferences.asideReplies !== false;
	if (row.type !== MENTIONED) return false;
	const viaTeam = Boolean(mentionMetadata(row)?.teamId);
	return viaTeam ? preferences.teamMentions : preferences.mentions;
}

/**
 * Whether the person is looking at the session a mention or aside reply is
 * about, so it raises no alert (and no badge): that session is on screen on
 * this machine, in a visible and focused window. Anywhere else it alerts,
 * even while AO's window is focused on another session.
 */
export function cloudSessionWatched(
	route: { hostId?: string; sessionId?: string },
	sessionId: string | undefined,
	attention: { visible: boolean; focused: boolean },
): boolean {
	return Boolean(sessionId) && !route.hostId && route.sessionId === sessionId && attention.visible && attention.focused;
}

/**
 * One desktop alert per row per thing that raised it: a repeated mention
 * (same row, a newer message) alerts again; the same event replayed does not.
 */
export function cloudAlertKey(row: Pick<CloudCpNotification, "id" | "eventId" | "updatedAt">): string {
	return `${row.id}:${row.eventId ?? row.updatedAt}`;
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
