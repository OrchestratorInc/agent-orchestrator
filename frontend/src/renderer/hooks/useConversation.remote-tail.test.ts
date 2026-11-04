import { QueryClient, type InfiniteData } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ConversationItem, ConversationSnapshot, ConversationTurn } from "../types/conversation";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));
vi.mock("../lib/host-clients", () => ({ clientForSessionHost: () => ({ GET: getMock }) }));

import { conversationQueryKey, mergeRemoteConversationTail, refreshRemoteConversation, remoteConversationFloor } from "./useConversation";

const turn = (id: string, state: ConversationTurn["state"], extra: Partial<ConversationTurn> = {}): ConversationTurn =>
	({ id, state, requestedAt: `2026-11-03T00:00:0${id.slice(-1)}Z`, ...extra });
const message = (id: string, turnId: string, sequence: number, text = id): ConversationItem =>
	({ kind: "message", id, turnId, sequence, revision: 1, role: "user", origin: "human", text, content: [], createdAt: "" }) as unknown as ConversationItem;
const tool = (id: string, turnId: string, sequence: number, status = "completed"): ConversationItem =>
	({ kind: "activity", id, turnId, sequence, revision: 1, activityKind: "command", status, summary: id, createdAt: "" }) as unknown as ConversationItem;
const page = (turns: ConversationTurn[], items: ConversationItem[], extra: Partial<ConversationSnapshot> = {}): ConversationSnapshot =>
	({
		conversationId: "c1", sessionId: "s1", harness: "codex", mode: "chat", controller: { state: "busy" },
		turns, items, latestSequence: Math.max(0, ...items.map((item) => item.sequence)),
		oldestSequence: items[0]?.sequence ?? 1, hasMoreBefore: false, settings: {}, activeBranchId: "b1", nativeForkAvailableAfterSequence: 0, ...extra,
	}) as ConversationSnapshot;

// t1 settled at 1-3; t2 running at 4-6 with a running tool at 5.
const prior = page(
	[turn("t1", "completed"), turn("t2", "running")],
	[message("m1", "t1", 1), tool("a1", "t1", 2), message("r1", "t1", 3), message("m2", "t2", 4), tool("a2", "t2", 5, "running"), message("r2", "t2", 6, "partial")],
	{ hasMoreBefore: true, oldestSequence: 1 },
);

describe("remote conversation tail", () => {
	it("puts the floor under the newest turn, open turns and unsettled activities", () => {
		expect(remoteConversationFloor(prior)).toBe(3);
		const idle = page([turn("t1", "completed"), turn("t2", "completed")], [message("m1", "t1", 1), tool("x", "t1", 2, "running"), message("m2", "t2", 3)]);
		expect(remoteConversationFloor(idle)).toBe(1);
		expect(remoteConversationFloor(page([], []))).toBe(0);
	});

	it("replaces the rows above the floor and keeps the rows below it", () => {
		const tail = page([turn("t2", "running")], [message("m2", "t2", 4), tool("a2", "t2", 5, "completed"), message("r2", "t2", 6, "partial and more"), tool("a3", "t2", 7)], { controller: { state: "busy" }, oldestSequence: 4 });
		const merged = mergeRemoteConversationTail(prior, tail, 3)!;
		expect(merged.items.map((item) => item.id)).toEqual(["m1", "a1", "r1", "m2", "a2", "r2", "a3"]);
		expect(merged.items.find((item) => item.id === "r2")).toMatchObject({ text: "partial and more" });
		expect(merged.turns.map((entry) => entry.id)).toEqual(["t1", "t2"]);
		// The cursor for older pages stays where the live page had it.
		expect(merged).toMatchObject({ oldestSequence: 1, hasMoreBefore: true, latestSequence: 7 });
	});

	it("refuses a tail when a turn changed, history was taken back, the branch moved or rows are missing", () => {
		const settled = page([turn("t2", "completed")], [message("m2", "t2", 4)]);
		expect(mergeRemoteConversationTail(prior, settled, 3)).toBeUndefined();
		const started = page([turn("t2", "running"), turn("t3", "queued")], [message("m2", "t2", 4)]);
		expect(mergeRemoteConversationTail(prior, started, 3)).toBeUndefined();
		const undone = page([turn("t2", "running", { rolledBack: true })], []);
		expect(mergeRemoteConversationTail(prior, undone, 3)).toBeUndefined();
		expect(mergeRemoteConversationTail(prior, page([turn("t2", "running")], [], { activeBranchId: "b2" }), 3)).toBeUndefined();
		expect(mergeRemoteConversationTail(prior, page([turn("t2", "running")], [], { hasMoreBefore: true }), 3)).toBeUndefined();
	});
});

const wire = (snapshot: Partial<Record<string, unknown>>) => ({
	data: { conversationId: "c1", sessionId: "s1", mode: "chat", controller: "busy", latestSequence: 6, activeBranchId: "b1", turns: [], messages: [], activities: [], ...snapshot },
});
const wireTurn = (id: string, state: string) => ({ id, state, requestedAt: `2026-11-03T00:00:0${id.slice(-1)}Z` });
const wireMessage = (id: string, turnId: string, sequence: number, text = id) => ({ kind: "message", id, turnId, sequence, revision: 1, role: "user", origin: "human", text, createdAt: "" });

describe("refreshRemoteConversation", () => {
	let client: QueryClient;
	const key = conversationQueryKey("s1", "box");
	beforeEach(() => {
		getMock.mockReset();
		client = new QueryClient();
		client.setQueryData<InfiniteData<ConversationSnapshot>>(key, { pages: [prior, page([], [])], pageParams: [undefined, 1] });
	});

	it("reads only the tail and merges it into the live page", async () => {
		getMock.mockResolvedValueOnce(wire({ afterSequence: 3, oldestSequence: 4, turns: [wireTurn("t2", "running")], messages: [wireMessage("m2", "t2", 4), wireMessage("r2", "t2", 6, "more")] }));
		await refreshRemoteConversation(client, "s1", "box");
		expect(getMock).toHaveBeenCalledOnce();
		expect(getMock.mock.calls[0][1].params.query).toEqual({ afterSequence: 3, limit: 30, outputLimit: 512 });
		const live = client.getQueryData<InfiniteData<ConversationSnapshot>>(key)!;
		expect(live.pages).toHaveLength(2);
		expect(live.pages[0].items.map((item) => item.id)).toEqual(["m1", "a1", "r1", "m2", "r2"]);
	});

	it("reads the whole live page when the tail cannot be merged", async () => {
		getMock
			.mockResolvedValueOnce(wire({ afterSequence: 3, turns: [wireTurn("t2", "completed")], messages: [wireMessage("m2", "t2", 4)] }))
			.mockResolvedValueOnce(wire({ oldestSequence: 4, turns: [wireTurn("t2", "completed")], messages: [wireMessage("m2", "t2", 4)] }));
		await refreshRemoteConversation(client, "s1", "box");
		expect(getMock).toHaveBeenCalledTimes(2);
		expect(getMock.mock.calls[1][1].params.query).toEqual({ beforeSequence: undefined, limit: 30, outputLimit: 512 });
	});

	it("uses the answer of a daemon that ignores afterSequence as the live page", async () => {
		getMock.mockResolvedValueOnce(wire({ oldestSequence: 4, turns: [wireTurn("t2", "completed")], messages: [wireMessage("m2", "t2", 4)] }));
		await refreshRemoteConversation(client, "s1", "box");
		expect(getMock).toHaveBeenCalledOnce();
	});
});
