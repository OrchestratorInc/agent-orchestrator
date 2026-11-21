import { beforeEach, describe, expect, it, vi } from "vitest";

const storage = vi.hoisted(() => new Map<string, string>());
vi.mock("@react-native-async-storage/async-storage", () => ({
	default: {
		getItem: async (key: string) => storage.get(key) ?? null,
		setItem: async (key: string, value: string) => { storage.set(key, value); },
	},
}));

beforeEach(() => {
	storage.clear();
	vi.resetModules();
});

describe("read state", () => {
	// Everything that existed before the first launch counts as read.
	it("reads sessions as unread only once they change after the baseline", async () => {
		const { isUnread } = await import("./readState");
		const read = { baseline: 1_000, readAt: {} };
		expect(isUnread(read, "mac:a", 500)).toBe(false);
		expect(isUnread(read, "mac:a", 1_500)).toBe(true);
		expect(isUnread(read, "mac:a", Number.NaN)).toBe(false);
	});

	it("marks a session read up to its latest change, and keeps that across launches", async () => {
		storage.set("ao.sidebar.readState.v1", JSON.stringify({ baseline: 1_000, readAt: {} }));
		const first = await import("./readState");
		await first.loadReadState();
		first.markRead("mac:a", 2_000);
		// An older change never moves the mark back.
		first.markRead("mac:a", 1_200);
		expect(JSON.parse(storage.get("ao.sidebar.readState.v1")!)).toEqual({ baseline: 1_000, readAt: { "mac:a": 2_000 } });

		vi.resetModules();
		const second = await import("./readState");
		await second.loadReadState();
		const stored = JSON.parse(storage.get("ao.sidebar.readState.v1")!);
		expect(second.isUnread(stored, "mac:a", 1_900)).toBe(false);
		expect(second.isUnread(stored, "mac:a", 2_100)).toBe(true);
	});

	it("takes the latest change a person would notice", async () => {
		const { changedAt } = await import("./readState");
		expect(changedAt({ lastEventAt: "2026-11-21T10:00:00Z", updatedAt: "2026-11-21T09:00:00Z" })).toBe(Date.parse("2026-11-21T10:00:00Z"));
		expect(changedAt({ updatedAt: null, lastActivityAt: "2026-11-21T08:00:00Z" })).toBe(Date.parse("2026-11-21T08:00:00Z"));
	});
});
