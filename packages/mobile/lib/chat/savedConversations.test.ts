import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn(), multiRemove: vi.fn() } }));

import { createConversationSaver, nextIndex, SAVED_CONVERSATIONS } from "./savedConversations";
import type { ConversationPage } from "./snapshot";

afterEach(() => vi.useRealTimers());

describe("saved chat pages", () => {
	it("keep the most recently saved chats and drop the oldest", () => {
		const index = Array.from({ length: SAVED_CONVERSATIONS }, (_, n) => `chat-${n}`);
		expect(nextIndex(index, "new")).toEqual({ keep: ["new", ...index.slice(0, SAVED_CONVERSATIONS - 1)], drop: [`chat-${SAVED_CONVERSATIONS - 1}`] });
		expect(nextIndex(index, "chat-5").drop).toEqual([]);
	});

	it("are written once a streaming chat settles, the newest page per chat", async () => {
		vi.useFakeTimers();
		const writes: Array<[string, number]> = [];
		const saver = createConversationSaver(async (key, page) => { writes.push([key, page.latestSequence]); }, 5_000);
		const page = (latestSequence: number) => ({ latestSequence } as ConversationPage);
		saver.schedule("a", page(1));
		saver.schedule("a", page(2));
		saver.schedule("b", page(7));
		await vi.advanceTimersByTimeAsync(1_000);
		expect(writes).toEqual([]);
		await vi.advanceTimersByTimeAsync(1_500);
		expect(writes).toEqual([["a", 2], ["b", 7]]);
		saver.schedule("a", page(3));
		await saver.flush();
		expect(writes.at(-1)).toEqual(["a", 3]);
	});
});
