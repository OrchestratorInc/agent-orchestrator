import { beforeEach, expect, it, vi } from "vitest";

const storage = new Map<string, string>();
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => storage.get(key) ?? null,
	setItem: async (key: string, value: string) => void storage.set(key, value),
} }));

import { CloudError } from "./errors";
import { cloudNotificationRecord, fetchCloudNotificationPage, hideCloudNotifications, markCloudNotificationsRead } from "./notificationFeed";
import type { CloudNotification } from "./types";

const notification = (id: string, overrides: Partial<CloudNotification> = {}): CloudNotification => ({
	id, source: "cloud", orgId: "o", sessionId: "s1", projectId: "p1", type: "needs_input", title: "Agent needs input", body: "Approve npm install",
	status: "unread", createdAt: "2026-11-02T00:00:00Z", updatedAt: "2026-11-02T00:00:00Z", ...overrides,
});

beforeEach(() => storage.clear());

it("lists cloud notifications as the screen's records, without the ones hidden on this phone", async () => {
	await hideCloudNotifications("o", ["gone"]);
	const listNotifications = vi.fn(async () => ({ items: [notification("n1", { metadata: { prUrl: "https://github.com/a/b/pull/1" } }), notification("gone")], page: { hasMore: true, nextCursor: "c2" }, unreadCount: 2, latestSequence: 9 }));
	const result = await fetchCloudNotificationPage({ listNotifications }, "o", undefined, 50);
	expect(result).toEqual({ page: { notifications: [expect.objectContaining({ id: "n1", sessionId: "s1", projectId: "p1", prUrl: "https://github.com/a/b/pull/1", type: "needs_input" })], nextCursor: "c2", unreadCount: 2 } });
	expect(cloudNotificationRecord(notification("x", { sessionId: undefined })).sessionId).toBe("");
});

it("reports a failed page in words instead of throwing", async () => {
	const listNotifications = vi.fn(async () => { throw new CloudError("down", { status: 0 }); });
	await expect(fetchCloudNotificationPage({ listNotifications }, "o", undefined, 50)).resolves.toEqual({ error: expect.stringMatching(/Couldn't reach Orchestrator Cloud/) });
});

it("treats an already-read notification as read", async () => {
	const markNotificationRead = vi.fn(async (_org: string, id: string) => {
		if (id === "old") throw new CloudError("gone", { status: 404 });
		return { updated: 1 };
	});
	await expect(markCloudNotificationsRead({ markNotificationRead }, "o", ["n1", "old"])).resolves.toBeUndefined();
	const failing = vi.fn(async () => { throw new CloudError("down", { status: 500 }); });
	await expect(markCloudNotificationsRead({ markNotificationRead: failing }, "o", ["n1"])).rejects.toBeInstanceOf(CloudError);
});

// Clearing two rows quickly starts two hides before either is written: each
// must read the other's write, or the first notification comes back.
it("keeps every hide when two run at once", async () => {
	await Promise.all([hideCloudNotifications("o", ["n1"]), hideCloudNotifications("o", ["n2"])]);
	expect(JSON.parse(storage.get("ao.cloud.hiddenNotifications.o") ?? "[]")).toEqual(["n1", "n2"]);
});
