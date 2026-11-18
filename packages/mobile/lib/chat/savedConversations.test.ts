import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const stored = new Map<string, string>();
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: vi.fn(async (key: string) => stored.get(key) ?? null),
	setItem: vi.fn(async (key: string, value: string) => { stored.set(key, value); }),
	removeItem: vi.fn(async (key: string) => { stored.delete(key); }),
	multiRemove: vi.fn(async (keys: string[]) => { for (const key of keys) stored.delete(key); }),
	getAllKeys: vi.fn(async () => [...stored.keys()]),
} }));
vi.mock("expo-file-system", () => import("./savedConversations.fakes").then((fakes) => fakes.fileSystemModule));
vi.mock("expo-crypto", () => import("./savedConversations.fakes").then((fakes) => fakes.cryptoModule));
vi.mock("expo-secure-store", () => import("./savedConversations.fakes").then((fakes) => fakes.secureStoreModule));

import {
	createConversationSaver,
	forgetSavedConversations,
	loadSavedConversation,
	SAVED_CONVERSATIONS,
	SAVED_PAGE_KEEP_MS,
	SAVED_PAGE_MAX_BYTES,
	SAVED_TOTAL_MAX_BYTES,
	savedConversationMachines,
	savedFileKey,
	savedFileName,
	savedPageEvictions,
	savedPageView,
} from "./savedConversations";
import { disk, resetSavedChatFakes, savedFileNames, secrets } from "./savedConversations.fakes";
import type { ConversationPage } from "./snapshot";

const page = (text: string, sessionId = "s") => ({ sessionId, conversationId: "c", latestSequence: 1, items: [{ kind: "message", id: "m", text }], turns: [] } as unknown as ConversationPage);
const fileOf = (key: string) => `cache/saved-chats/${savedFileName(key)}`;
const savedKeys = () => savedFileNames().map((name) => savedFileKey(name)).sort();
const diskBytes = () => [...disk.values()].reduce((sum, file) => sum + file.bytes.byteLength, 0);
const files = (count: number, size = 1, now = 1_000) => Array.from({ length: count }, (_, n) => ({ name: `chat-${n}.page`, size, modified: now - n }));

/** Saves one page through a saver, written as soon as it is flushed. */
async function save(key: string, value: ConversationPage) {
	const saver = createConversationSaver();
	saver.schedule(key, value);
	await saver.flush();
}

beforeEach(() => {
	stored.clear();
	resetSavedChatFakes();
});
afterEach(() => vi.useRealTimers());

describe("saved chat pages", () => {
	it("are files in the cache directory, encrypted, and read back as the page", async () => {
		await save("host.a/chat", page("the secret plan"));
		expect(savedFileNames()).toEqual(["host.a%2Fchat.page"]);
		expect(new TextDecoder().decode(disk.get(fileOf("host.a/chat"))!.bytes)).not.toContain("the secret plan");
		expect([...stored.keys()]).toEqual([]);
		expect((await loadSavedConversation("host.a/chat"))?.items).toEqual([{ kind: "message", id: "m", text: "the secret plan" }]);
		expect(await loadSavedConversation("host.a/other")).toBeNull();
	});

	it("keep the most recently saved chats and drop the oldest", async () => {
		for (let n = 0; n <= SAVED_CONVERSATIONS; n++) await save(`host.a/chat-${n}`, page("hi"));
		expect(savedKeys()).toHaveLength(SAVED_CONVERSATIONS);
		expect(savedKeys()).not.toContain("host.a/chat-0");
		expect(savedKeys()).toContain(`host.a/chat-${SAVED_CONVERSATIONS}`);
		// Saving a kept chat again makes it the newest.
		await save("host.a/chat-1", page("again"));
		await save("host.a/new", page("hi"));
		expect(savedKeys()).toContain("host.a/chat-1");
		expect(savedKeys()).not.toContain("host.a/chat-2");
	});

	it("evict the oldest chats once large pages pass the total budget", async () => {
		const large = "中".repeat(Math.floor(SAVED_PAGE_MAX_BYTES / 3) - 200);
		for (let n = 0; n < 8; n++) await save(`host.a/chat-${n}`, page(large));
		const kept = savedKeys();
		expect(diskBytes()).toBeLessThanOrEqual(SAVED_TOTAL_MAX_BYTES);
		expect(kept.length).toBeLessThan(8);
		expect(kept).toContain("host.a/chat-7");
		expect(kept).not.toContain("host.a/chat-0");
	});

	it("skip a CJK page that fits by character count but not by UTF-8 bytes", async () => {
		const overhead = new TextEncoder().encode(JSON.stringify({ v: 2, savedAt: Date.now(), page: page("") })).byteLength;
		// Three bytes a character: one past the byte cap, far under it in characters.
		const chars = Math.floor((SAVED_PAGE_MAX_BYTES - overhead) / 3) + 1;
		expect(JSON.stringify(page("中".repeat(chars))).length).toBeLessThan(SAVED_PAGE_MAX_BYTES);
		await save("host.a/too-big", page("中".repeat(chars)));
		expect(savedKeys()).toEqual([]);

		await save("host.a/fits", page("中".repeat(chars - 2)));
		expect(savedKeys()).toEqual(["host.a/fits"]);
	});

	it("are not shown, and are deleted, once a week old", async () => {
		vi.useFakeTimers({ toFake: ["Date"] });
		vi.setSystemTime(new Date("2026-11-01T00:00:00Z"));
		await save("host.a/old", page("last week"));
		await save("host.a/other-old", page("last week"));
		vi.setSystemTime(Date.now() + SAVED_PAGE_KEEP_MS + 1_000);

		expect(await loadSavedConversation("host.a/old")).toBeNull();
		expect(savedKeys()).toEqual(["host.a/other-old"]);
		// The next write clears the rest.
		await save("host.a/new", page("today"));
		expect(savedKeys()).toEqual(["host.a/new"]);
	});

	it("load a file that does not decrypt as nothing saved, and delete it", async () => {
		await save("host.a/chat", page("mine"));
		// Another chat's file under this chat's name: bound to its own chat, it does not open.
		disk.set(fileOf("host.a/copy"), disk.get(fileOf("host.a/chat"))!);
		disk.set(fileOf("host.a/cut"), { bytes: disk.get(fileOf("host.a/chat"))!.bytes.slice(0, 40), modified: Date.now() });
		expect(await loadSavedConversation("host.a/copy")).toBeNull();
		expect(await loadSavedConversation("host.a/cut")).toBeNull();
		expect(savedKeys()).toEqual(["host.a/chat"]);
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

	it("are named by their chat's key, escaped, and only their names read back as chats", () => {
		expect(savedFileName("http.192.168.1.20.3011/ao-1")).toBe("http.192.168.1.20.3011%2Fao-1.page");
		expect(savedFileKey("http.192.168.1.20.3011%2Fao-1.page")).toBe("http.192.168.1.20.3011/ao-1");
		expect(savedFileName(`host.a/${"x".repeat(300)}`)).toBeNull();
		expect(savedFileKey("stray.tmp")).toBeNull();
		expect(savedFileKey("%E0%A4%A.page")).toBeNull();
	});
});

describe("which saved files go", () => {
	it("past the newest chats by count", () => {
		expect(savedPageEvictions(files(SAVED_CONVERSATIONS + 2), "chat-0.page", 1_000)).toEqual(["chat-12.page", "chat-13.page"]);
	});

	it("past the byte budget, the least recently saved first", () => {
		const big = SAVED_TOTAL_MAX_BYTES / 4;
		expect(savedPageEvictions(files(6, big), "chat-5.page", 1_000)).toEqual(["chat-3.page", "chat-4.page"]);
	});

	it("anything that is not a page, and pages past a week old", () => {
		const now = SAVED_PAGE_KEEP_MS + 10_000;
		const listed = [{ name: "a.page", size: 1, modified: now }, { name: "stray.tmp", size: 1, modified: now }, { name: "old.page", size: 1, modified: 9_000 }];
		expect(savedPageEvictions(listed, "a.page", now)).toEqual(["stray.tmp", "old.page"]);
	});
});

describe("a saved page shown before the live read", () => {
	const midTurn = {
		...page("hi"),
		controller: { state: "busy" },
		turns: [{ id: "t1", state: "running", requestedAt: "2026-11-01T10:00:00Z" }, { id: "t0", state: "completed", requestedAt: "2026-11-01T09:00:00Z" }],
		items: [
			{ kind: "message", id: "m" },
			{ kind: "activity", id: "cmd", activityKind: "command", status: "running" },
			{ kind: "activity", id: "ask", activityKind: "user_input", status: "pending", requestId: "r2" },
			{ kind: "activity", id: "ok", activityKind: "approval", status: "pending", requestId: "r1" },
			{ kind: "activity", id: "old", activityKind: "approval", status: "resolved", requestId: "r0" },
		],
	} as unknown as ConversationPage;

	it("offers no open request and shows nothing as working", () => {
		const view = savedPageView(midTurn);
		expect(view.controller.state).toBe("ready");
		expect(view.turns.map((turn) => turn.state)).toEqual(["recovered", "completed"]);
		expect(view.items.map((item) => item.id)).toEqual(["m", "cmd", "old"]);
		expect(view.items.find((item) => item.id === "cmd")).toMatchObject({ status: "completed" });
	});

	it("is the page itself when nothing in it was live", () => {
		const settled = page("done");
		expect(savedPageView(settled)).toBe(settled);
	});

	it("loads as that read-only view", async () => {
		await save("host.a/mid-turn", midTurn);
		const loaded = await loadSavedConversation("host.a/mid-turn");
		expect(loaded?.items.map((item) => item.id)).toEqual(["m", "cmd", "old"]);
		expect(loaded?.turns[0]?.state).toBe("recovered");
	});
});

describe("forgetting a machine", () => {
	const host = { id: "h_one", name: "One", platform: "darwin", endpoints: [{ kind: "lan" as const, host: "host-one", port: 3011, secure: false }], lastConnected: 1 };

	it("deletes that machine's saved chats and keeps the others", async () => {
		await save("host.h_one/s1", page("one"));
		await save("http.host-one.3011/s2", page("migrated"));
		await save("host.h_two/s3", page("two"));

		await forgetSavedConversations(host);

		expect(savedKeys()).toEqual(["host.h_two/s3"]);
		expect(await loadSavedConversation("host.h_one/s1")).toBeNull();
		expect(await loadSavedConversation("host.h_two/s3")).not.toBeNull();
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
		}, 5_000, async (matches, all) => { order.push(`remove:${["host.a/x", "host.b/x"].filter(matches).join(",")}:${all}`); });

		saver.schedule("host.a/under-way", page("x"));
		await vi.advanceTimersByTimeAsync(2_500);
		saver.schedule("host.a/waiting", page("x"));
		saver.schedule("host.b/kept", page("x"));
		const forgotten = saver.forget(["host.a"]);
		finishWrite();
		await forgotten;
		await vi.advanceTimersByTimeAsync(10_000);

		expect(order).toEqual(["write:host.a/under-way", "remove:host.a/x:false", "write:host.b/kept"]);
	});

	it("deletes every saved chat and the key when no machine is named", async () => {
		await save("host.h_one/s1", page("one"));
		await save("host.h_two/s3", page("two"));
		await forgetSavedConversations(null);
		expect(savedKeys()).toEqual([]);
		expect(secrets.size).toBe(0);

		// The next save makes a new key.
		await save("host.h_one/s1", page("again"));
		expect(secrets.size).toBe(1);
		expect(await loadSavedConversation("host.h_one/s1")).not.toBeNull();
	});
});

describe("pages older builds kept in AsyncStorage", () => {
	const legacy = () => {
		stored.set("ao.chat.saved.v1", JSON.stringify([["host.a/s", 10]]));
		stored.set("ao.chat.saved.v1.host.a/s", JSON.stringify({ v: 1, savedAt: Date.now(), page: page("old") }));
		stored.set("ao.chat.draft.host.a/s", "kept");
	};
	// Cleared once a launch, so each case starts from a fresh module.
	const fresh = async () => {
		vi.resetModules();
		return import("./savedConversations");
	};

	it("are removed the first time a chat is opened, leaving the rest of the app's data", async () => {
		legacy();
		const saved = await fresh();
		expect(await saved.loadSavedConversation("host.a/s")).toBeNull();
		await saved.clearLegacySavedPages();
		expect([...stored.keys()]).toEqual(["ao.chat.draft.host.a/s"]);
	});

	it("are removed before the first page is written", async () => {
		legacy();
		const saved = await fresh();
		const saver = saved.createConversationSaver();
		saver.schedule("host.a/s", page("new"));
		await saver.flush();
		expect([...stored.keys()]).toEqual(["ao.chat.draft.host.a/s"]);
	});
});
