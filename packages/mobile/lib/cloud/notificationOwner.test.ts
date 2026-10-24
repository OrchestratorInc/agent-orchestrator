import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CloudApiError, type CloudNotification, type NotificationEventPage } from "@aoagents/cloud-client";
import { createCloudNotificationOwner } from "./notificationOwner";

function row(overrides: Partial<CloudNotification> = {}): CloudNotification {
	return { id: "notice-1", orgId: "org-1", recipientUserId: "user-1", source: "cloud", sessionId: "session-1", projectId: "project-1", type: "needs_input", title: "Needs you", body: "Waiting", status: "unread", metadata: {}, createdAt: "2026-10-15T09:00:00Z", updatedAt: "2026-10-15T09:00:00Z", ...overrides };
}
function page(items: CloudNotification[], unreadCount = items.filter((item) => item.status === "unread").length, nextCursor?: string) {
	return { items, unreadCount, page: { hasMore: !!nextCursor, nextCursor }, latestSequence: 999 };
}
function deferred<T>() {
	let resolve!: (value: T) => void;
	const promise = new Promise<T>((done) => { resolve = done; });
	return { promise, resolve };
}
function setup() {
	let current = true;
	const saved = new Map<string, string>();
	const storage = { getItem: async (key: string) => saved.get(key) ?? null, setItem: async (key: string, value: string) => { saved.set(key, value); } };
	const client = {
		listNotifications: vi.fn(async (_org: string, _options?: object) => page([row()])),
		listNotificationEvents: vi.fn(async (_org: string, _options?: object): Promise<NotificationEventPage> => ({ items: [], hasMore: false })),
		markNotificationRead: vi.fn(async (_org: string, _id: string, _options?: object) => ({ updated: 1 })),
		markAllNotificationsRead: vi.fn(async (_org: string, _options?: object) => ({ updated: 1 })),
	};
	const options = { client, storage, orgId: "org-1", userId: "user-1", scopeKey: '["https://cloud.test","user-1","org-1"]', isCurrent: () => current };
	const owner = createCloudNotificationOwner(options);
	return { owner, client, saved, options, invalidate: () => { current = false; } };
}
const owners: ReturnType<typeof createCloudNotificationOwner>[] = [];
async function start(fixture: ReturnType<typeof setup>) {
	owners.push(fixture.owner);
	fixture.owner.start();
	await vi.advanceTimersByTimeAsync(0);
}
beforeEach(() => vi.useFakeTimers());
afterEach(() => { for (const owner of owners.splice(0)) owner.stop(); vi.useRealTimers(); });

describe("Cloud notification inbox owner", () => {
	it("loads the authoritative list and count without acknowledging rows", async () => {
		const fixture = setup();
		fixture.client.listNotifications.mockResolvedValue(page([row()], 7));
		await start(fixture);
		expect(fixture.owner.getSnapshot()).toMatchObject({ items: [row()], unreadCount: 7, loading: false, error: null });
		expect(fixture.client.markNotificationRead).not.toHaveBeenCalled();
		expect(fixture.client.listNotificationEvents).toHaveBeenCalledWith("org-1", expect.objectContaining({ after: 0 }));
	});
	it("rebuilds the loaded pagination window instead of dropping older pages", async () => {
		const fixture = setup();
		const older = row({ id: "older", createdAt: "2026-10-14T09:00:00Z" });
		let changed = false;
		fixture.client.listNotifications.mockImplementation(async (_org, options) => {
			const cursor = (options as { cursor?: string })?.cursor;
			return cursor ? page([older], 7) : page([row({ title: changed ? "Updated" : "Needs you" })], 7, "page-2");
		});
		await start(fixture);
		await fixture.owner.loadMore();
		changed = true;
		await fixture.owner.refresh();
		expect(fixture.owner.getSnapshot().items.map((item) => [item.id, item.title])).toEqual([["notice-1", "Updated"], ["older", "Needs you"]]);
		expect(fixture.owner.getSnapshot().unreadCount).toBe(7);
	});
	it("refreshes read changes periodically without notification events", async () => {
		const fixture = setup();
		await start(fixture);
		fixture.client.listNotifications.mockResolvedValue(page([row({ status: "read" })], 0));
		await vi.advanceTimersByTimeAsync(30_000);
		expect(fixture.owner.getSnapshot()).toMatchObject({ unreadCount: 0, items: [row({ status: "read" })] });
	});
	it("aborts active requests and ignores late background results, then refreshes on resume", async () => {
		const fixture = setup();
		await start(fixture);
		const pending = deferred<ReturnType<typeof page>>();
		fixture.client.listNotifications.mockImplementationOnce(() => pending.promise);
		const refresh = fixture.owner.refresh();
		const options = fixture.client.listNotifications.mock.calls.at(-1)?.[1] as { signal: AbortSignal };
		fixture.owner.stop();
		expect(options.signal.aborted).toBe(true);
		pending.resolve(page([row({ title: "Late" })], 99));
		await refresh;
		await vi.advanceTimersByTimeAsync(60_000);
		expect(fixture.owner.getSnapshot().unreadCount).toBe(1);
		fixture.client.listNotifications.mockResolvedValue(page([row({ title: "Resumed" })], 3));
		fixture.owner.start();
		await vi.advanceTimersByTimeAsync(0);
		expect(fixture.owner.getSnapshot()).toMatchObject({ unreadCount: 3, items: [row({ title: "Resumed" })] });
	});
	it("marks only selected IDs and refreshes the server count, treating already-read as success", async () => {
		const fixture = setup();
		await start(fixture);
		fixture.client.markNotificationRead.mockRejectedValueOnce(new CloudApiError(404, { error: "Not Found", code: "NOT_FOUND", message: "Already read", requestId: "req" }));
		fixture.client.listNotifications.mockResolvedValue(page([row({ status: "read" })], 0));
		await fixture.owner.markRead(["notice-1", "notice-1"]);
		expect(fixture.client.markNotificationRead).toHaveBeenCalledTimes(1);
		expect(fixture.client.markNotificationRead).toHaveBeenCalledWith("org-1", "notice-1", expect.objectContaining({ signal: expect.any(AbortSignal) }));
		expect(fixture.owner.getSnapshot().unreadCount).toBe(0);
	});
	it("persists device clears, rolls back failures, and permanently revives newer unread rows", async () => {
		const fixture = setup();
		await start(fixture);
		fixture.client.markNotificationRead.mockRejectedValueOnce(new Error("offline"));
		await expect(fixture.owner.clearOne(row())).rejects.toThrow("offline");
		expect(fixture.owner.getSnapshot().items).toHaveLength(1);
		fixture.client.listNotifications.mockResolvedValue(page([row({ status: "read" })], 0));
		await fixture.owner.clearOne(row());
		expect(fixture.owner.getSnapshot().items).toEqual([]);
		const restarted = createCloudNotificationOwner(fixture.options);
		owners.push(restarted); restarted.start(); await vi.advanceTimersByTimeAsync(0);
		expect(restarted.getSnapshot().items).toEqual([]);
		fixture.client.listNotifications.mockResolvedValue(page([row({ updatedAt: "2026-10-15T10:00:00Z" })], 1));
		await restarted.refresh();
		expect(restarted.getSnapshot().items).toHaveLength(1);
		fixture.client.listNotifications.mockResolvedValue(page([row({ status: "read", updatedAt: "2026-10-15T09:00:00Z" })], 0));
		await restarted.refresh();
		expect(restarted.getSnapshot().items).toHaveLength(1);
	});
	it("rejects obsolete-owner actions and never adopts another recipient's rows", async () => {
		const fixture = setup();
		fixture.client.listNotifications.mockResolvedValue(page([row({ recipientUserId: "user-2" })], 20));
		await start(fixture);
		expect(fixture.owner.getSnapshot().items).toEqual([]);
		expect(fixture.owner.getSnapshot().unreadCount).toBe(0);
		expect(fixture.owner.getSnapshot().error).toBeTruthy();
		fixture.invalidate();
		await expect(fixture.owner.markAllRead()).rejects.toThrow();
		expect(fixture.client.markAllNotificationsRead).not.toHaveBeenCalled();
	});
	it("drains ordered event pages from a safe boundary and refreshes authoritative state", async () => {
		const fixture = setup();
		const event = (sequence: number) => ({ sequence, orgId: "org-1", recipientUserId: "user-1", kind: "notification_created" as const, eventId: `event-${sequence}`, notification: row(), createdAt: row().createdAt });
		fixture.client.listNotificationEvents.mockImplementation(async (_org, options) => {
			const after = (options as { after: number }).after;
			return after === 0 ? { items: [event(2), event(1)], hasMore: true } : after === 2 ? { items: [event(2), event(3)], hasMore: false } : { items: [], hasMore: false };
		});
		await start(fixture);
		await vi.advanceTimersByTimeAsync(2_000);
		expect(fixture.client.listNotificationEvents.mock.calls.map(([, options]) => (options as { after: number }).after)).toEqual([0, 2, 3]);
		expect(fixture.owner.getSnapshot().items).toHaveLength(1);
	});
	it("does not advance past invalid events and backs off instead of spinning on a stalled page", async () => {
		const fixture = setup();
		fixture.client.listNotificationEvents.mockResolvedValue({ items: [], hasMore: true });
		await start(fixture);
		expect(fixture.owner.getSnapshot().error).toContain("cursor did not advance");
		await vi.advanceTimersByTimeAsync(999);
		expect(fixture.client.listNotificationEvents).toHaveBeenCalledTimes(1);
		await vi.advanceTimersByTimeAsync(1);
		expect(fixture.client.listNotificationEvents).toHaveBeenCalledTimes(2);
		await vi.advanceTimersByTimeAsync(1_999);
		expect(fixture.client.listNotificationEvents).toHaveBeenCalledTimes(2);
		await vi.advanceTimersByTimeAsync(1);
		expect(fixture.client.listNotificationEvents).toHaveBeenCalledTimes(3);
	});
	it("honors Retry-After for rate limited event polling", async () => {
		const fixture = setup();
		fixture.client.listNotificationEvents.mockRejectedValueOnce(new CloudApiError(429, { error: "Rate limited", code: "RATE_LIMITED", message: "Wait", requestId: "req" }, 8));
		await start(fixture);
		await vi.advanceTimersByTimeAsync(7_999);
		expect(fixture.client.listNotificationEvents).toHaveBeenCalledTimes(1);
		await vi.advanceTimersByTimeAsync(1);
		expect(fixture.client.listNotificationEvents).toHaveBeenCalledTimes(2);
	});
	it("rejects invalid recipient and unsafe sequence events without saving a skip cursor", async () => {
		const fixture = setup();
		const event = { sequence: Number.MAX_SAFE_INTEGER + 1, orgId: "org-1", recipientUserId: "user-2", kind: "notification_created" as const, eventId: "event", notification: row(), createdAt: row().createdAt };
		fixture.client.listNotificationEvents.mockResolvedValueOnce({ items: [event], hasMore: false });
		await start(fixture);
		await vi.advanceTimersByTimeAsync(1_000);
		expect(fixture.client.listNotificationEvents.mock.calls.map(([, options]) => (options as { after: number }).after)).toEqual([0, 0]);
	});
	it("refreshes again when an event arrived after an in-flight list took its snapshot", async () => {
		const fixture = setup();
		const first = deferred<ReturnType<typeof page>>();
		fixture.client.listNotifications.mockImplementationOnce(() => first.promise).mockResolvedValue(page([row({ title: "New event" })], 2));
		fixture.client.listNotificationEvents.mockResolvedValueOnce({ items: [{ sequence: 1, orgId: "org-1", recipientUserId: "user-1", kind: "notification_created" as const, eventId: "event", notification: row(), createdAt: row().createdAt }], hasMore: false });
		await start(fixture);
		first.resolve(page([], 0));
		await vi.advanceTimersByTimeAsync(0);
		expect(fixture.owner.getSnapshot()).toMatchObject({ unreadCount: 2, items: [row({ title: "New event" })] });
	});
	it("never publishes or persists a late clear from a replaced account", async () => {
		const fixture = setup();
		await start(fixture);
		const pending = deferred<{ updated: number }>();
		fixture.client.markNotificationRead.mockImplementationOnce(() => pending.promise);
		const clearing = fixture.owner.clearOne(row());
		await vi.advanceTimersByTimeAsync(0);
		fixture.invalidate(); fixture.owner.stop();
		pending.resolve({ updated: 1 });
		await expect(clearing).rejects.toThrow();
		expect(fixture.saved.size).toBe(0);
	});
	it.each(["one", "all", "clear"] as const)("requests an authoritative snapshot after %s completes despite an older in-flight list", async (kind) => {
		const fixture = setup();
		await start(fixture);
		const oldList = deferred<ReturnType<typeof page>>();
		fixture.client.listNotifications.mockImplementationOnce(() => oldList.promise).mockResolvedValue(page([row({ status: "read" })], 0));
		const refreshing = fixture.owner.refresh();
		const mutation = kind === "one" ? fixture.owner.markRead(["notice-1"]) : kind === "all" ? fixture.owner.markAllRead() : fixture.owner.clearOne(row());
		await vi.advanceTimersByTimeAsync(0);
		oldList.resolve(page([row()], 1));
		await refreshing; await mutation;
		expect(fixture.owner.getSnapshot().unreadCount).toBe(0);
		expect(fixture.owner.getSnapshot().items).toEqual(kind === "clear" ? [] : [row({ status: "read" })]);
	});
	it("bounds per-device clear markers to the most recent server timestamps", async () => {
		const fixture = setup();
		await start(fixture);
		for (let index = 0; index < 201; index++) {
			await fixture.owner.clearOne(row({ id: `cleared-${index}`, status: "read", updatedAt: new Date(Date.parse("2026-10-15T09:00:00Z") + index * 1_000).toISOString() }));
		}
		const stored = JSON.parse([...fixture.saved.values()][0]);
		expect(Object.keys(stored)).toHaveLength(200);
		expect(stored["cleared-0"]).toBeUndefined();
		expect(stored["cleared-200"]).toBe(Date.parse("2026-10-15T09:03:20Z"));
	});
	it("keeps the previously loaded oldest row when new notifications shift pagination", async () => {
		const fixture = setup();
		const oldest = row({ id: "oldest", createdAt: "2026-10-14T09:00:00Z" });
		fixture.client.listNotifications.mockImplementation(async (_org, options) => (options as { cursor?: string })?.cursor ? page([oldest], 2) : page([row()], 2, "second"));
		await start(fixture);
		await fixture.owner.loadMore();
		fixture.client.listNotifications.mockImplementation(async (_org, options) => {
			const cursor = (options as { cursor?: string })?.cursor;
			return cursor === "third" ? page([oldest], 3) : cursor === "second" ? page([row()], 3, "third") : page([row({ id: "new", createdAt: "2026-10-15T10:00:00Z" })], 3, "second");
		});
		await fixture.owner.refresh();
		expect(fixture.owner.getSnapshot().items.map((item) => item.id)).toEqual(["new", "notice-1", "oldest"]);
	});
	it("does not overwrite a clear with an older persisted map when foregrounding", async () => {
		const fixture = setup();
		await start(fixture);
		fixture.owner.stop();
		const oldStorage = deferred<string | null>();
		fixture.options.storage.getItem = async () => oldStorage.promise;
		fixture.owner.start();
		await vi.advanceTimersByTimeAsync(0);
		fixture.client.listNotifications.mockResolvedValue(page([row({ status: "read" })], 0));
		await fixture.owner.clearOne(row());
		expect(fixture.owner.getSnapshot().items).toEqual([]);
		oldStorage.resolve("{}");
		await vi.advanceTimersByTimeAsync(0);
		expect(fixture.owner.getSnapshot().items).toEqual([]);
	});
	it("waits for the first persisted clear map before accepting a mutation", async () => {
		const fixture = setup();
		const initial = deferred<string | null>();
		fixture.options.storage.getItem = async () => initial.promise;
		fixture.owner.start(); owners.push(fixture.owner);
		const clearing = fixture.owner.clearOne(row());
		await vi.advanceTimersByTimeAsync(0);
		expect(fixture.client.markNotificationRead).not.toHaveBeenCalled();
		initial.resolve(JSON.stringify({ "other-notice": Date.parse("2026-10-15T08:00:00Z") }));
		fixture.client.listNotifications.mockResolvedValue(page([row({ status: "read" })], 0));
		await clearing;
		expect(JSON.parse([...fixture.saved.values()][0])).toEqual({ "other-notice": Date.parse("2026-10-15T08:00:00Z"), "notice-1": Date.parse("2026-10-15T09:00:00Z") });
	});
});
