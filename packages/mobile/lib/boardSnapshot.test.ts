import { afterEach, describe, expect, it, vi } from "vitest";

const items = vi.hoisted(() => new Map<string, string>());
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => items.get(key) ?? null,
	setItem: async (key: string, value: string) => { items.set(key, value); },
	removeItem: async (key: string) => { items.delete(key); },
} }));

import { BOARD_KEEP_MS, BOARD_MAX_BYTES, boardKey, createBoardSaver, forgetBoard, parseBoard, serializeBoard, utf8ByteLength, type SavedBoard } from "./boardSnapshot";

const board = (overrides: Partial<SavedBoard> = {}): Omit<SavedBoard, "v"> => ({
	machine: "host.a",
	savedAt: 1_000,
	sessions: [{ id: "web-1", displayName: "Fix flaky login test" } as never],
	orchestrators: [],
	projects: [{ id: "web-app", name: "web-app" }],
	archive: { total: 3, byProject: { "web-app": 3 } },
	stats: {},
	notificationsUnread: 2,
	...overrides,
});

afterEach(() => vi.useRealTimers());

describe("the saved board", () => {
	it("reads back for its own machine only, and only while recent", () => {
		const text = serializeBoard(board())!;
		expect(parseBoard(text, "host.a", 2_000)?.sessions.map((session) => session.id)).toEqual(["web-1"]);
		expect(parseBoard(text, "host.b", 2_000)).toBeNull();
		expect(parseBoard(text, "host.a", 1_000 + BOARD_KEEP_MS)).toBeNull();
		expect(parseBoard("{broken", "host.a")).toBeNull();
		expect(parseBoard(null, "host.a")).toBeNull();
	});

	it("is not saved when too large for one stored value", () => {
		const huge = board({ sessions: [{ id: "x".repeat(BOARD_MAX_BYTES) } as never] });
		expect(serializeBoard(huge)).toBeNull();
	});

	// Android's limit is in bytes: a board of names in another script is far
	// larger stored than its character count says.
	it("measures the limit in UTF-8 bytes, not characters", () => {
		expect(utf8ByteLength("a")).toBe(1);
		expect(utf8ByteLength("é")).toBe(2);
		expect(utf8ByteLength("修")).toBe(3);
		expect(utf8ByteLength("😀")).toBe(4);
		expect(utf8ByteLength("a修😀")).toBe(8);
		const overhead = serializeBoard(board({ sessions: [{ id: "" } as never] }))!.length;
		// Under the limit in characters, over it in bytes.
		const chars = Math.floor((BOARD_MAX_BYTES - overhead) / 2);
		const wide = board({ sessions: [{ id: "修".repeat(chars) } as never] });
		expect(JSON.stringify({ v: 1, ...wide }).length).toBeLessThan(BOARD_MAX_BYTES);
		expect(serializeBoard(wide)).toBeNull();
		// One byte short of the limit is still kept.
		const fits = board({ sessions: [{ id: "x".repeat(BOARD_MAX_BYTES - overhead - 1) } as never] });
		expect(serializeBoard(fits)).not.toBeNull();
	});

	it("drops a saved board whose rows or archive are not shaped as a live answer", () => {
		const read = (overrides: Record<string, unknown>) => parseBoard(JSON.stringify({ v: 1, ...board(), ...overrides }), "host.a", 2_000);
		expect(read({})).not.toBeNull();
		expect(read({ sessions: [null] })).toBeNull();
		expect(read({ sessions: [{ displayName: "no id" }] })).toBeNull();
		expect(read({ orchestrators: ["x"] })).toBeNull();
		expect(read({ projects: [{ id: "web-app" }] })).toBeNull();
		expect(read({ archive: { total: 3 } })).toBeNull();
		expect(read({ archive: { total: 3, byProject: null } })).toBeNull();
		expect(read({ archive: { total: 3, byProject: { "web-app": "3" } } })).toBeNull();
		expect(read({ archive: { total: "3", byProject: {} } })).toBeNull();
		expect(parseBoard("[]", "host.a", 2_000)).toBeNull();
		expect(parseBoard("null", "host.a", 2_000)).toBeNull();
		expect(read({ projects: null, archive: null, stats: [1] })).toMatchObject({ projects: null, archive: null, stats: {} });
	});

	it("is written at most every so often, the newest one, and at once on leaving the app", async () => {
		vi.useFakeTimers();
		const writes: Array<[string, number]> = [];
		const saver = createBoardSaver(async (key, text) => { writes.push([key, JSON.parse(text).notificationsUnread]); }, 15_000);
		saver.schedule(board({ notificationsUnread: 1 }));
		await vi.advanceTimersByTimeAsync(0);
		saver.schedule(board({ notificationsUnread: 2 }));
		saver.schedule(board({ notificationsUnread: 3 }));
		await vi.advanceTimersByTimeAsync(5_000);
		expect(writes).toEqual([[boardKey("host.a"), 1]]);
		await saver.flush();
		expect(writes).toEqual([[boardKey("host.a"), 1], [boardKey("host.a"), 3]]);
	});

	// Switching machines inside the throttle window: the first machine's
	// newer board is still saved, not replaced by the second's.
	it("keeps a board waiting for each machine", async () => {
		vi.useFakeTimers();
		const writes: Array<[string, number]> = [];
		const saver = createBoardSaver(async (key, text) => { writes.push([key, JSON.parse(text).notificationsUnread]); }, 15_000);
		saver.schedule(board({ notificationsUnread: 1 }));
		await vi.advanceTimersByTimeAsync(0);
		saver.schedule(board({ notificationsUnread: 2 }));
		saver.schedule(board({ machine: "host.b", notificationsUnread: 7 }));
		await saver.flush();
		expect(writes).toEqual([[boardKey("host.a"), 1], [boardKey("host.a"), 2], [boardKey("host.b"), 7]]);
		// Forgetting one leaves the other's waiting save in place.
		saver.schedule(board({ notificationsUnread: 3 }));
		saver.schedule(board({ machine: "host.b", notificationsUnread: 8 }));
		await saver.forget("host.a");
		await vi.advanceTimersByTimeAsync(15_000);
		expect(writes.slice(3)).toEqual([[boardKey("host.b"), 8]]);
	});

	// A poll inside the throttle window leaves a save waiting: forgetting the
	// machine must not see it write the board back afterwards.
	it("never writes a forgotten machine's board back", async () => {
		vi.useFakeTimers();
		items.clear();
		const saver = createBoardSaver(undefined, 15_000);
		saver.schedule(board());
		await vi.advanceTimersByTimeAsync(0);
		expect(items.has(boardKey("host.a"))).toBe(true);
		saver.schedule(board({ notificationsUnread: 5 }));
		await forgetBoard("host.a");
		await vi.advanceTimersByTimeAsync(30_000);
		await saver.flush();
		expect(items.has(boardKey("host.a"))).toBe(false);
		// Another machine's save still goes.
		saver.schedule(board({ machine: "host.b" }));
		await saver.flush();
		expect(items.has(boardKey("host.b"))).toBe(true);
	});

	it("waits for a write already running before deleting the board", async () => {
		items.clear();
		let finish = () => {};
		const saver = createBoardSaver(async (key, text) => { await new Promise<void>((resolve) => { finish = resolve; }); items.set(key, text); }, 0);
		saver.schedule(board());
		const writing = saver.flush();
		await Promise.resolve();
		const forgetting = forgetBoard("host.a");
		finish();
		await writing;
		await forgetting;
		expect(items.has(boardKey("host.a"))).toBe(false);
	});
});
