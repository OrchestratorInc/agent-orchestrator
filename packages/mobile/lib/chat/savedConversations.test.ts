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

import AsyncStorage from "@react-native-async-storage/async-storage";
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
import { pairSavedChatMachines, resetSavedChatMachines } from "./savedChatMachines";
import { disk, interruptedWrites, resetSavedChatFakes, savedFileNames, secrets, undeletable, unreadableSecrets } from "./savedConversations.fakes";
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
	resetSavedChatMachines();
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

	it("are not shown once a week old, and the next write deletes them", async () => {
		vi.useFakeTimers({ toFake: ["Date"] });
		vi.setSystemTime(new Date("2026-11-01T00:00:00Z"));
		await save("host.a/old", page("last week"));
		await save("host.a/other-old", page("last week"));
		vi.setSystemTime(Date.now() + SAVED_PAGE_KEEP_MS + 1_000);

		expect(await loadSavedConversation("host.a/old")).toBeNull();
		// The next write clears the rest.
		await save("host.a/new", page("today"));
		expect(savedKeys()).toEqual(["host.a/new"]);
	});

	it("load a file that does not decrypt as nothing saved", async () => {
		await save("host.a/chat", page("mine"));
		// Another chat's file under this chat's name: bound to its own chat, it does not open.
		disk.set(fileOf("host.a/copy"), disk.get(fileOf("host.a/chat"))!);
		disk.set(fileOf("host.a/cut"), { bytes: disk.get(fileOf("host.a/chat"))!.bytes.slice(0, 40), modified: Date.now() });
		expect(await loadSavedConversation("host.a/copy")).toBeNull();
		expect(await loadSavedConversation("host.a/cut")).toBeNull();
		// Left for the next write of that chat, or pruning, to replace or remove.
		expect(savedKeys()).toEqual(["host.a/chat", "host.a/copy", "host.a/cut"]);
	});

	it("keep the previous page when its replacement is cut short", async () => {
		await save("host.a/chat", page("before"));
		// Killed while writing the replacement: only part of the `.tmp` lands.
		interruptedWrites.add(`${fileOf("host.a/chat")}.tmp`);
		await save("host.a/chat", page("after"));
		expect(savedFileNames()).toEqual(["host.a%2Fchat.page", "host.a%2Fchat.page.tmp"]);
		expect((await loadSavedConversation("host.a/chat"))?.items[0]).toMatchObject({ text: "before" });

		// Another chat's write clears the stale `.tmp` beside its page.
		await save("host.a/other", page("x"));
		expect(savedFileNames()).toEqual(["host.a%2Fchat.page", "host.a%2Fother.page"]);
		// The chat's next write replaces the page.
		await save("host.a/chat", page("next"));
		expect(savedFileNames()).toEqual(["host.a%2Fchat.page", "host.a%2Fother.page"]);
		expect((await loadSavedConversation("host.a/chat"))?.items[0]).toMatchObject({ text: "next" });
	});

	it("load a whole replacement left when the app was killed as it was moved into place", async () => {
		await save("host.a/chat", page("after"));
		// Killed between the move's delete and its rename: only the whole `.tmp` is left.
		disk.set(`${fileOf("host.a/chat")}.tmp`, disk.get(fileOf("host.a/chat"))!);
		disk.delete(fileOf("host.a/chat"));
		expect((await loadSavedConversation("host.a/chat"))?.items[0]).toMatchObject({ text: "after" });
		// Another chat's write keeps it, as the chat's page.
		await save("host.a/other", page("x"));
		expect((await loadSavedConversation("host.a/chat"))?.items[0]).toMatchObject({ text: "after" });
		expect(savedKeys()).toEqual(["host.a/chat", "host.a/other"]);
	});

	it("keep a whole replacement beside a page that does not open", async () => {
		await save("host.a/chat", page("whole"));
		// The `.tmp` is whole; the page beside it no longer opens.
		const whole = disk.get(fileOf("host.a/chat"))!;
		disk.set(`${fileOf("host.a/chat")}.tmp`, whole);
		disk.set(fileOf("host.a/chat"), { bytes: whole.bytes.slice(0, 40), modified: whole.modified });
		expect((await loadSavedConversation("host.a/chat"))?.items[0]).toMatchObject({ text: "whole" });

		// Another chat's write puts the `.tmp` in the page's place instead of deleting it.
		await save("host.a/other", page("x"));
		expect(savedFileNames()).toEqual(["host.a%2Fchat.page", "host.a%2Fother.page"]);
		expect((await loadSavedConversation("host.a/chat"))?.items[0]).toMatchObject({ text: "whole" });
	});

	it("make a new key when the stored one does not read back, as after a backup restored onto another phone", async () => {
		// A fresh module holds no key in memory, so it reads the stored one.
		vi.resetModules();
		const fresh = await import("./savedConversations");
		secrets.set("ao.chat.savedPagesKey", "restored");
		unreadableSecrets.add("ao.chat.savedPagesKey");
		disk.set(fileOf("host.a/old"), { bytes: new Uint8Array(64), modified: Date.now() });

		// A load treats it as nothing saved and leaves the key alone.
		expect(await fresh.loadSavedConversation("host.a/old")).toBeNull();
		expect(secrets.get("ao.chat.savedPagesKey")).toBe("restored");

		// A save replaces it, and saving goes on.
		const saver = fresh.createConversationSaver();
		saver.schedule("host.a/chat", page("after the restore"));
		await saver.flush();
		expect(secrets.get("ao.chat.savedPagesKey")).not.toBe("restored");
		expect((await fresh.loadSavedConversation("host.a/chat"))?.items[0]).toMatchObject({ text: "after the restore" });
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
		expect(savedFileKey("http.192.168.1.20.3011%2Fao-1.page.tmp")).toBe("http.192.168.1.20.3011/ao-1");
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

	it("a replacement beside its page, but not one standing in for a missing page", () => {
		const listed = [{ name: "a.page", size: 1, modified: 3 }, { name: "a.page.tmp", size: 1, modified: 2 }, { name: "b.page.tmp", size: 1, modified: 1 }];
		expect(savedPageEvictions(listed, "a.page", 10)).toEqual(["a.page.tmp"]);
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

	// ChatMarkdown reads the flag: a streaming reply holds back its diagrams and code highlighting.
	it("draws a reply saved mid-stream as a finished one", () => {
		const streamingReply = { kind: "message", id: "reply", role: "assistant", text: "Half a ", streaming: true };
		const done = { kind: "message", id: "done", role: "assistant", text: "Done.", streaming: false };
		// Saved while the agent was busy.
		const busy = savedPageView({ ...midTurn, items: [done, streamingReply] } as unknown as ConversationPage);
		expect(busy.items).toEqual([done, { ...streamingReply, streaming: false }]);
		expect(busy.items.filter((item) => item.kind === "message" && item.streaming)).toEqual([]);
		// Saved after the agent read as ready, with the reply's last update still streaming.
		const idle = { ...page("x"), controller: { state: "ready" }, items: [done, streamingReply] } as unknown as ConversationPage;
		const view = savedPageView(idle);
		expect(view).not.toBe(idle);
		expect(view.controller).toEqual({ state: "ready" });
		expect(view.items).toEqual([done, { ...streamingReply, streaming: false }]);
		expect(view.items.filter((item) => item.kind === "message" && item.streaming)).toEqual([]);
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
		// A replacement a kill left behind goes with its chat.
		disk.set(`${fileOf("host.h_one/s1")}.tmp`, disk.get(fileOf("host.h_one/s1"))!);

		await forgetSavedConversations(host);

		expect(savedFileNames()).toEqual(["host.h_two%2Fs3.page"]);
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

	it("saves nothing of it from a read that answers after, and still saves other machines' chats", async () => {
		await save("host.h_one/s1", page("one"));
		await forgetSavedConversations(host);

		// A refresh under way when the machine was forgotten answers now.
		await save("host.h_one/s1", page("late"));
		await save("http.host-one.3011/s2", page("late"));
		await save("host.h_two/s3", page("two"));

		expect(savedKeys()).toEqual(["host.h_two/s3"]);
	});

	it("drops a write that waited behind another when the machine was forgotten", async () => {
		const saver = createConversationSaver();
		saver.schedule("host.h_two/first", page("two"));
		saver.schedule("host.h_one/s1", page("one"));
		// Both are queued, one behind the other, when the machine is forgotten.
		const flushed = saver.flush();
		void forgetSavedConversations(host);
		await flushed;
		await saver.flush();

		expect(savedKeys()).toEqual(["host.h_two/first"]);
	});

	it("leaves no file from a write already under way when the machine was forgotten", async () => {
		vi.useFakeTimers();
		let finishWrite!: () => void;
		const writing = new Promise<void>((resolve) => { finishWrite = resolve; });
		// The native write lands only after the forget began; the removal still runs after it.
		const saver = createConversationSaver(async (key) => {
			await writing;
			disk.set(fileOf(key), { bytes: new Uint8Array([1]), modified: Date.now() });
		});
		saver.schedule("host.h_one/s1", page("one"));
		await vi.advanceTimersByTimeAsync(2_500);
		const forgotten = saver.forget(savedConversationMachines(host));
		finishWrite();
		await forgotten;

		expect(savedFileNames()).toEqual([]);
	});

	it("saves its chats again once it is paired again", async () => {
		await forgetSavedConversations(host);
		await save("host.h_one/s1", page("late"));
		expect(savedKeys()).toEqual([]);

		pairSavedChatMachines(savedConversationMachines(host));
		await save("host.h_one/s1", page("paired again"));

		expect((await loadSavedConversation("host.h_one/s1"))?.items[0]).toMatchObject({ text: "paired again" });
	});

	it("deletes the rest of the machine's chats when one file will not go, then reports it", async () => {
		await save("host.h_one/s1", page("one"));
		await save("host.h_one/s2", page("one"));
		await save("http.host-one.3011/s4", page("migrated"));
		await save("host.h_two/s3", page("two"));
		undeletable.add(fileOf("host.h_one/s1"));

		await expect(forgetSavedConversations(host)).rejects.toThrow("cannot delete");
		expect(savedKeys()).toEqual(["host.h_one/s1", "host.h_two/s3"]);
	});

	it("deletes every saved chat and the key when no machine is named", async () => {
		// A fresh module holds no key in memory, so the pages below must store one.
		vi.resetModules();
		const fresh = await import("./savedConversations");
		const saver = fresh.createConversationSaver();
		saver.schedule("host.h_one/s1", page("one"));
		saver.schedule("host.h_two/s3", page("two"));
		await saver.flush();
		expect(savedKeys()).toEqual(["host.h_one/s1", "host.h_two/s3"]);
		expect([...secrets.keys()]).toEqual(["ao.chat.savedPagesKey"]);

		await fresh.forgetSavedConversations(null);
		expect(savedKeys()).toEqual([]);
		expect(secrets.size).toBe(0);

		// Nothing is saved again until a machine is paired again, and then only its chats.
		saver.schedule("host.h_one/s1", page("late"));
		await saver.flush();
		expect(savedKeys()).toEqual([]);
		(await import("./savedChatMachines")).pairSavedChatMachines(["host.h_one"]);

		// The next save makes a new key.
		saver.schedule("host.h_one/s1", page("again"));
		saver.schedule("host.h_two/s3", page("late"));
		await saver.flush();
		expect(secrets.size).toBe(1);
		expect(savedKeys()).toEqual(["host.h_one/s1"]);
		expect(await fresh.loadSavedConversation("host.h_one/s1")).not.toBeNull();
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
		vi.mocked(AsyncStorage.multiRemove).mockClear();
		expect(await saved.loadSavedConversation("host.a/s")).toBeNull();
		// Opening the chat starts the cleanup itself, without waiting on it.
		await vi.waitFor(() => expect([...stored.keys()]).toEqual(["ao.chat.draft.host.a/s"]));
		expect(AsyncStorage.multiRemove).toHaveBeenCalledWith(["ao.chat.saved.v1", "ao.chat.saved.v1.host.a/s"]);
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
