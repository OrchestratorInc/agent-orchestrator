import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const stored = new Map<string, string>();
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: vi.fn(async (key: string) => stored.get(key) ?? null),
	setItem: vi.fn(async (key: string, value: string) => { stored.set(key, value); }),
	removeItem: vi.fn(async (key: string) => { stored.delete(key); }),
	multiRemove: vi.fn(async (keys: string[]) => { for (const key of keys) stored.delete(key); }),
	getAllKeys: vi.fn(async () => [...stored.keys()]),
} }));
vi.mock("expo-secure-store", () => ({ getItemAsync: async () => null, setItemAsync: async () => {}, deleteItemAsync: async () => {} }));

import {
	createConversationSaver,
	forgetSavedConversations,
	loadSavedConversation,
	nextIndex,
	SAVED_CONVERSATIONS,
	SAVED_PAGE_MAX_BYTES,
	SAVED_TOTAL_MAX_BYTES,
	savedConversationMachines,
	utf8Bytes,
} from "./savedConversations";
import type { ConversationPage } from "./snapshot";

const INDEX_KEY = "ao.chat.saved.v1";
const PAGE_PREFIX = "ao.chat.saved.v1.";
const entries = (count: number, bytes = 1) => Array.from({ length: count }, (_, n) => ({ key: `chat-${n}`, bytes }));
const page = (text: string, sessionId = "s") => ({ sessionId, conversationId: "c", latestSequence: 1, items: [{ kind: "message", id: "m", text }], turns: [] } as unknown as ConversationPage);
const savedKeys = () => [...stored.keys()].filter((key) => key.startsWith(PAGE_PREFIX)).map((key) => key.slice(PAGE_PREFIX.length)).sort();
const indexKeys = () => (JSON.parse(stored.get(INDEX_KEY) ?? "[]") as Array<[string, number]>).map(([key]) => key);

/** Saves one page through a saver, written as soon as it is flushed. */
async function save(key: string, value: ConversationPage) {
	const saver = createConversationSaver();
	saver.schedule(key, value);
	await saver.flush();
}

beforeEach(() => stored.clear());
afterEach(() => vi.useRealTimers());

describe("saved chat pages", () => {
	it("keep the most recently saved chats and drop the oldest", () => {
		const index = entries(SAVED_CONVERSATIONS);
		expect(nextIndex(index, "new", 1)).toEqual({ keep: [{ key: "new", bytes: 1 }, ...index.slice(0, SAVED_CONVERSATIONS - 1)], drop: [`chat-${SAVED_CONVERSATIONS - 1}`] });
		expect(nextIndex(index, "chat-5", 1).drop).toEqual([]);
	});

	it("drop the least recently saved chats to stay within the byte budget", () => {
		const big = SAVED_TOTAL_MAX_BYTES / 4;
		const { keep, drop } = nextIndex(entries(5, big), "new", big);
		expect(keep.map((entry) => entry.key)).toEqual(["new", "chat-0", "chat-1", "chat-2"]);
		expect(drop).toEqual(["chat-3", "chat-4"]);
		expect(keep.reduce((sum, entry) => sum + entry.bytes, 0)).toBeLessThanOrEqual(SAVED_TOTAL_MAX_BYTES);
	});

	it("count multibyte text in UTF-8 bytes, not UTF-16 units", () => {
		expect(utf8Bytes("abc")).toBe(3);
		expect(utf8Bytes("é")).toBe(2);
		expect(utf8Bytes("中文")).toBe(6);
		expect(utf8Bytes("😀")).toBe(4);
	});

	it("skip a CJK page that fits by character count but not by bytes", async () => {
		const overhead = utf8Bytes(JSON.stringify({ v: 1, savedAt: Date.now(), page: page("") }));
		// Three bytes a character: one past the byte cap, far under it in characters.
		const chars = Math.floor((SAVED_PAGE_MAX_BYTES - overhead) / 3) + 1;
		expect(JSON.stringify(page("中".repeat(chars))).length).toBeLessThan(SAVED_PAGE_MAX_BYTES);
		await save("host.a/too-big", page("中".repeat(chars)));
		expect(savedKeys()).toEqual([]);

		await save("host.a/fits", page("中".repeat(chars - 2)));
		expect(savedKeys()).toEqual(["host.a/fits"]);
	});

	it("evict the oldest chats once large pages pass the total budget", async () => {
		const large = "中".repeat(Math.floor(SAVED_PAGE_MAX_BYTES / 3) - 200);
		for (let n = 0; n < 8; n++) await save(`host.a/chat-${n}`, page(large));
		const kept = savedKeys();
		const total = kept.reduce((sum, key) => sum + utf8Bytes(stored.get(PAGE_PREFIX + key)!), 0);
		expect(total).toBeLessThanOrEqual(SAVED_TOTAL_MAX_BYTES);
		expect(kept.length).toBeLessThan(8);
		expect(kept).toContain("host.a/chat-7");
		expect(kept).not.toContain("host.a/chat-0");
		expect(indexKeys().sort()).toEqual(kept);
	});

	it("clear pages an interrupted save left outside the index", async () => {
		stored.set(PAGE_PREFIX + "host.a/orphan", JSON.stringify({ v: 1, savedAt: Date.now(), page: page("left behind") }));
		for (let n = 0; n < SAVED_CONVERSATIONS; n++) await save(`host.a/chat-${n}`, page("hi"));
		expect(savedKeys()).not.toContain("host.a/orphan");
		expect(savedKeys()).toHaveLength(SAVED_CONVERSATIONS);
	});

	it("are written once a streaming chat settles, the newest page per chat", async () => {
		vi.useFakeTimers();
		const writes: Array<[string, number]> = [];
		const saver = createConversationSaver(async (key, saved) => { writes.push([key, saved.latestSequence]); }, 5_000);
		const at = (latestSequence: number) => ({ latestSequence } as ConversationPage);
		saver.schedule("a", at(1));
		saver.schedule("a", at(2));
		saver.schedule("b", at(7));
		await vi.advanceTimersByTimeAsync(1_000);
		expect(writes).toEqual([]);
		await vi.advanceTimersByTimeAsync(1_500);
		expect(writes).toEqual([["a", 2], ["b", 7]]);
		saver.schedule("a", at(3));
		await saver.flush();
		expect(writes.at(-1)).toEqual(["a", 3]);
	});
});

describe("forgetting a machine", () => {
	const host = { id: "h_one", name: "One", platform: "darwin", endpoints: [{ kind: "lan" as const, host: "host-one", port: 3011, secure: false }], lastConnected: 1 };

	it("removes that machine's saved chats and index entries and keeps the others", async () => {
		await save("host.h_one/s1", page("one"));
		await save("http.host-one.3011/s2", page("migrated"));
		await save("host.h_two/s3", page("two"));
		stored.set(PAGE_PREFIX + "host.h_one/orphan", "{}");

		await forgetSavedConversations(host);

		expect(savedKeys()).toEqual(["host.h_two/s3"]);
		expect(indexKeys()).toEqual(["host.h_two/s3"]);
		expect(await loadSavedConversation("host.h_one/s1")).toBeNull();
	});

	it("names the machine by its host id and by each address it was reached at before it had one", () => {
		expect(savedConversationMachines(host)).toEqual(["host.h_one", "http.host-one.3011"]);
	});

	it("drops a write still waiting for it, and runs after one already under way", async () => {
		vi.useFakeTimers();
		const order: string[] = [];
		let finishWrite!: () => void;
		const writing = new Promise<void>((resolve) => { finishWrite = resolve; });
		const saver = createConversationSaver(async (key) => {
			order.push(`write:${key}`);
			if (key === "host.a/under-way") await writing;
		}, 5_000, async (matches) => { order.push(`remove:${["host.a/x", "host.b/x"].filter(matches).join(",")}`); });

		saver.schedule("host.a/under-way", page("x"));
		await vi.advanceTimersByTimeAsync(2_500);
		saver.schedule("host.a/waiting", page("x"));
		saver.schedule("host.b/kept", page("x"));
		const forgotten = saver.forget(["host.a"]);
		finishWrite();
		await forgotten;
		await vi.advanceTimersByTimeAsync(10_000);

		expect(order).toEqual(["write:host.a/under-way", "remove:host.a/x", "write:host.b/kept"]);
	});

	it("removes every saved chat when no machine is named", async () => {
		await save("host.h_one/s1", page("one"));
		await save("host.h_two/s3", page("two"));
		await forgetSavedConversations(null);
		expect(savedKeys()).toEqual([]);
		expect(indexKeys()).toEqual([]);
	});
});
