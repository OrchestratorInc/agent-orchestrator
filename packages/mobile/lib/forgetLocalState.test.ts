import { beforeEach, describe, expect, it, vi } from "vitest";

const store = vi.hoisted(() => ({ items: new Map<string, string>() }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => store.items.get(key) ?? null,
	setItem: async (key: string, value: string) => { store.items.set(key, value); },
	removeItem: async (key: string) => { store.items.delete(key); },
} }));
vi.mock("expo-file-system", () => {
	class Directory { exists = false; delete() {} }
	return { Directory, File: class {}, Paths: { document: { path: "/docs" } } };
});
vi.mock("react-native", () => ({}));
vi.mock("expo-secure-store", () => ({ getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn() }));
vi.mock("./account", () => ({ loadAccount: async () => null }));
vi.mock("./hosts", () => ({ loadHosts: async () => [] }));

import { boardKey } from "./boardSnapshot";
import { addReviewComment, reviewComments, reviewKey } from "./files/reviewStore";
import { forgetAccountState, forgetMachineState } from "./forgetLocalState";
import { LAST_ROUTE_KEY } from "./lastRoute";
import { SPAWN_DRAFT_KEY } from "./spawnDraft";

const target = { path: "a.ts", side: "new" as const, line: 1 };
const draft = (owner: { hostId: string | null; accountId: string | null }) => JSON.stringify({ v: 1, savedAt: Date.now(), prompt: "p", attachments: [], owner });

beforeEach(() => store.items.clear());

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
});
