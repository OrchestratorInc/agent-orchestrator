import AsyncStorage from "@react-native-async-storage/async-storage";
import type { NotificationRecord } from "../api";
import type { CloudClient } from "./client";
import { cloudErrorMessage } from "./errors";
import type { CloudNotification } from "./types";

// Orchestrator Cloud's notifications join the machines' in the one
// Notifications screen, as the "Cloud" feed. The control plane has no delete
// route, so clearing one hides it on this phone and marks it read, as the
// desktop's Clear does.

const HIDDEN_LIMIT = 500;
const hiddenKey = (orgId: string) => `ao.cloud.hiddenNotifications.${orgId}`;

export async function hiddenCloudNotifications(orgId: string): Promise<Set<string>> {
	try {
		const raw = await AsyncStorage.getItem(hiddenKey(orgId));
		const parsed = raw ? JSON.parse(raw) : [];
		return new Set(Array.isArray(parsed) ? parsed.filter((id): id is string => typeof id === "string") : []);
	} catch {
		return new Set();
	}
}

// Each organization's hides run one after another, so each reads the last
// one's write: two cleared at once would otherwise both read the old list.
const hideChains = new Map<string, Promise<void>>();

export function hideCloudNotifications(orgId: string, ids: readonly string[]): Promise<void> {
	if (!ids.length) return Promise.resolve();
	const previous = hideChains.get(orgId) ?? Promise.resolve();
	const next = previous.then(async () => {
		const hidden = [...await hiddenCloudNotifications(orgId), ...ids];
		// Newest last; the oldest fall off once the list is long.
		const kept = [...new Set(hidden)].slice(-HIDDEN_LIMIT);
		await AsyncStorage.setItem(hiddenKey(orgId), JSON.stringify(kept)).catch(() => {});
	});
	hideChains.set(orgId, next);
	void next.finally(() => { if (hideChains.get(orgId) === next) hideChains.delete(orgId); });
	return next;
}

/** A cloud notification in the record shape the Notifications screen lists. */
export function cloudNotificationRecord(notification: CloudNotification): NotificationRecord {
	const prUrl = typeof notification.metadata?.prUrl === "string" ? notification.metadata.prUrl : "";
	return {
		id: notification.id,
		sessionId: notification.sessionId ?? "",
		projectId: notification.projectId ?? "",
		prUrl,
		type: notification.type,
		title: notification.title,
		body: notification.body,
		status: notification.status,
		createdAt: notification.createdAt,
	};
}

export type CloudFeedPage =
	| { page: { notifications: NotificationRecord[]; nextCursor?: string; unreadCount: number } }
	| { error: string };

/** One page of the Cloud feed, without the rows hidden on this phone. */
export async function fetchCloudNotificationPage(client: Pick<CloudClient, "listNotifications">, orgId: string, cursor: string | undefined, limit: number): Promise<CloudFeedPage> {
	try {
		const [page, hidden] = await Promise.all([
			client.listNotifications(orgId, { status: "all", limit, cursor }),
			hiddenCloudNotifications(orgId),
		]);
		return {
			page: {
				notifications: page.items.filter((item) => !hidden.has(item.id)).map(cloudNotificationRecord),
				nextCursor: page.page.hasMore ? page.page.nextCursor : undefined,
				unreadCount: page.unreadCount,
			},
		};
	} catch (cause) {
		return { error: cloudErrorMessage(cause) };
	}
}

/** Mark specific cloud notifications read; the control plane takes one at a time. */
export async function markCloudNotificationsRead(client: Pick<CloudClient, "markNotificationRead">, orgId: string, ids: readonly string[]): Promise<void> {
	const results = await Promise.allSettled(ids.map((id) => client.markNotificationRead(orgId, id)));
	// 404 means it was already read (or resolved): that is the outcome we wanted.
	const failed = results.find((result) => result.status === "rejected" && (result.reason as { status?: number })?.status !== 404);
	if (failed) throw (failed as PromiseRejectedResult).reason;
}
