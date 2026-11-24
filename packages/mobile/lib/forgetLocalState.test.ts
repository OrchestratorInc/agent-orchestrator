import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const store = vi.hoisted(() => ({ items: new Map<string, string>(), failRemove: new Map<string, number>(), failGet: new Map<string, number>() }));
const reviews = vi.hoisted(() => ({ flush: null as null | (() => Promise<void>) }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => {
		const failures = store.failGet.get(key) ?? 0;
		if (failures > 0) {
			store.failGet.set(key, failures - 1);
			throw new Error(`cannot read ${key}`);
		}
		return store.items.get(key) ?? null;
	},
	setItem: async (key: string, value: string) => { store.items.set(key, value); },
	removeItem: async (key: string) => {
		const failures = store.failRemove.get(key) ?? 0;
		if (failures > 0) {
			store.failRemove.set(key, failures - 1);
			throw new Error(`cannot remove ${key}`);
		}
		store.items.delete(key);
	},
} }));
vi.mock("expo-file-system", () => {
	class Directory { exists = false; delete() {} }
	return { Directory, File: class {}, Paths: { document: { path: "/docs" } } };
});
vi.mock("react-native", () => ({}));
vi.mock("expo-secure-store", () => ({ getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn() }));
vi.mock("./account", () => ({ loadAccount: async () => null }));
vi.mock("./hosts", () => ({ loadHosts: async () => [] }));
vi.mock("./files/reviewPersistence", () => ({ flushReviewPersistence: () => reviews.flush?.() ?? Promise.resolve() }));

import { boardKey } from "./boardSnapshot";
import { addReviewComment, reviewComments, reviewKey } from "./files/reviewStore";
import { forgetAccountState, forgetMachineState } from "./forgetLocalState";
import { LAST_ROUTE_KEY } from "./lastRoute";
import { SPAWN_DRAFT_KEY } from "./spawnDraft";

const target = { path: "a.ts", side: "new" as const, line: 1 };
const draft = (owner: { hostId: string | null; accountId: string | null }) => JSON.stringify({ v: 1, savedAt: Date.now(), prompt: "p", attachments: [], owner });

afterEach(() => vi.restoreAllMocks());
beforeEach(() => { store.items.clear(); store.failRemove.clear(); store.failGet.clear(); reviews.flush = null; vi.spyOn(console, "warn").mockImplementation(() => {}); });

describe("forgetting what the app kept across being closed", () => {
	it("leaves nothing of a forgotten machine's: board, New task draft, review comments, last screen", async () => {
		store.items.set(boardKey("host.machine-a"), "{}");
		store.items.set(boardKey("host.machine-b"), "{}");
		store.items.set(SPAWN_DRAFT_KEY, draft({ hostId: "machine-a", accountId: null }));
		store.items.set(LAST_ROUTE_KEY, JSON.stringify({ href: "/session/s1?hostId=machine-a", leftAt: 1 }));
		addReviewComment(reviewKey("machine-a", "s1"), { label: "x", feedback: "a's", target });
		addReviewComment(reviewKey("machine-b", "s1"), { label: "x", feedback: "b's", target });
		await forgetMachineState("machine-a");
		expect([...store.items.keys()]).toEqual([boardKey("host.machine-b")]);
		expect(reviewComments(reviewKey("machine-a", "s1"))).toEqual([]);
		expect(reviewComments(reviewKey("machine-b", "s1"))).toHaveLength(1);
	});

	it("leaves nothing of a signed-out account's for the next one", async () => {
		store.items.set(SPAWN_DRAFT_KEY, draft({ hostId: "orchestrator-cloud", accountId: "user-1" }));
		store.items.set(LAST_ROUTE_KEY, JSON.stringify({ href: "/session/c1?hostId=orchestrator-cloud", leftAt: 1 }));
		addReviewComment(reviewKey("orchestrator-cloud", "c1"), { label: "x", feedback: "cloud", target });
		await forgetAccountState("user-1");
		expect(store.items.size).toBe(0);
		expect(reviewComments(reviewKey("orchestrator-cloud", "c1"))).toEqual([]);
	});

	it("keeps another account's draft", async () => {
		store.items.set(SPAWN_DRAFT_KEY, draft({ hostId: "machine-a", accountId: "user-2" }));
		await forgetAccountState("user-1");
		expect(store.items.has(SPAWN_DRAFT_KEY)).toBe(true);
	});

	// The disconnect flow reports a forget that left something behind; a
	// cleanup that swallowed the failure let the machine vanish with its state kept.
	it("fails once every deletion ran, when one still could not be done", async () => {
		store.items.set(boardKey("host.machine-a"), "{}");
		store.items.set(SPAWN_DRAFT_KEY, draft({ hostId: "machine-a", accountId: null }));
		store.items.set(LAST_ROUTE_KEY, "{}");
		store.failRemove.set(boardKey("host.machine-a"), 2);
		await expect(forgetMachineState("machine-a")).rejects.toThrow("cannot remove ao.board.v1.host.machine-a");
		// The others still went.
		expect([...store.items.keys()]).toEqual([boardKey("host.machine-a")]);
		expect(console.warn).toHaveBeenCalled();

		store.items.set(SPAWN_DRAFT_KEY, draft({ hostId: null, accountId: "user-1" }));
		store.failRemove.set(LAST_ROUTE_KEY, 2);
		await expect(forgetAccountState("user-1")).rejects.toThrow(`cannot remove ${LAST_ROUTE_KEY}`);
		expect(store.items.has(SPAWN_DRAFT_KEY)).toBe(false);
	});

	it("tries a failed deletion again before giving up", async () => {
		store.items.set(boardKey("host.machine-a"), "{}");
		store.failRemove.set(boardKey("host.machine-a"), 1);
		await forgetMachineState("machine-a");
		expect(store.items.size).toBe(0);
	});

	// A draft that could not be read may still be there: tried again, and reported if it stays unreadable.
	it("tries a failed draft read again, and fails when it keeps failing", async () => {
		store.items.set(SPAWN_DRAFT_KEY, draft({ hostId: "machine-a", accountId: "user-1" }));
		store.failGet.set(SPAWN_DRAFT_KEY, 1);
		await forgetMachineState("machine-a");
		expect(store.items.has(SPAWN_DRAFT_KEY)).toBe(false);

		store.items.set(SPAWN_DRAFT_KEY, draft({ hostId: "machine-a", accountId: "user-1" }));
		store.failGet.set(SPAWN_DRAFT_KEY, 2);
		await expect(forgetAccountState("user-1")).rejects.toThrow(`cannot read ${SPAWN_DRAFT_KEY}`);
		expect(store.items.has(SPAWN_DRAFT_KEY)).toBe(true);
	});

	// Resolved only once the saved reviews no longer hold the forgotten ones,
	// so a phone that closes the app right after cannot bring them back.
	it("waits for the saved reviews to be written over before resolving", async () => {
		for (const forget of [() => forgetAccountState("user-1"), () => forgetMachineState("machine-a")]) {
			let finishWrite!: () => void;
			reviews.flush = () => new Promise<void>((resolve) => { finishWrite = resolve; });
			let done = false;
			const forgetting = forget().then(() => { done = true; });
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(done).toBe(false);
			finishWrite();
			await forgetting;
			expect(done).toBe(true);
		}
	});
});
