import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: vi.fn(), setItem: vi.fn() } }));

import { BOARD_KEEP_MS, BOARD_MAX_CHARS, boardKey, createBoardSaver, parseBoard, serializeBoard, type SavedBoard } from "./boardSnapshot";

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
		const huge = board({ sessions: [{ id: "x".repeat(BOARD_MAX_CHARS) } as never] });
		expect(serializeBoard(huge)).toBeNull();
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
});

