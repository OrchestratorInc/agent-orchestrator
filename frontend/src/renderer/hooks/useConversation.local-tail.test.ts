import { QueryClient, type InfiniteData } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ConversationItem, ConversationSnapshot, ConversationTurn } from "../types/conversation";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));
vi.mock("../lib/host-clients", () => ({ baseUrlForHost: () => undefined, clientForSessionHost: () => ({ GET: getMock }) }));

import { conversationQueryKey, refreshLocalConversationTail } from "./useConversation";

const turn = (id: string, state: ConversationTurn["state"]): ConversationTurn =>
	({ id, state, requestedAt: `2026-11-03T00:00:0${id.slice(-1)}Z` }) as ConversationTurn;
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

const wire = (snapshot: Partial<Record<string, unknown>>) => ({
	data: { conversationId: "c1", sessionId: "s1", mode: "chat", controller: "busy", latestSequence: 6, activeBranchId: "b1", turns: [], messages: [], activities: [], ...snapshot },
});
const wireTurn = (id: string, state: string) => ({ id, state, requestedAt: `2026-11-03T00:00:0${id.slice(-1)}Z` });
const wireMessage = (id: string, turnId: string, sequence: number, text = id) => ({ kind: "message", id, turnId, sequence, revision: 1, role: "user", origin: "human", text, createdAt: "" });

// t1 settled at 1-3 (floor 3); t2 running at 4-6 with a running tool at 5.
const live = page(
	[turn("t1", "completed"), turn("t2", "running")],
	[message("m1", "t1", 1), tool("a1", "t1", 2), message("r1", "t1", 3), message("m2", "t2", 4), tool("a2", "t2", 5, "running"), message("r2", "t2", 6, "partial")],
	{ hasMoreBefore: true, oldestSequence: 1 },
);
const older = page([turn("t0", "completed")], [message("m0", "t0", 0)]);

describe("refreshLocalConversationTail", () => {
	let client: QueryClient;
	const key = conversationQueryKey("s1");
	const cached = () => client.getQueryData<InfiniteData<ConversationSnapshot>>(key);
	beforeEach(() => {
		getMock.mockReset();
		client = new QueryClient();
		client.setQueryData<InfiniteData<ConversationSnapshot>>(key, { pages: [live, older], pageParams: [undefined, 1] });
	});

	it("reads only the rows above the floor, with whole outputs, and merges them into the live page", async () => {
		getMock.mockResolvedValueOnce(wire({ afterSequence: 3, oldestSequence: 4, turns: [wireTurn("t2", "running")], messages: [wireMessage("m2", "t2", 4), wireMessage("r2", "t2", 6, "partial and more")] }));
		await expect(refreshLocalConversationTail(client, "s1")).resolves.toBe(true);
		expect(getMock).toHaveBeenCalledOnce();
		// A local tail keeps whole outputs and asks for at most a live page.
		expect(getMock.mock.calls[0][1].params.query).toEqual({ afterSequence: 3, limit: 30 });
		const data = cached()!;
		expect(data.pages).toHaveLength(2);
		expect(data.pages[1]).toBe(older);
		expect(data.pages[0].items.map((item) => item.id)).toEqual(["m1", "a1", "r1", "m2", "r2"]);
		expect(data.pages[0].items.find((item) => item.id === "r2")).toMatchObject({ text: "partial and more" });
		expect(data.pages[0]).toMatchObject({ oldestSequence: 1, hasMoreBefore: true });
	});

	it("writes nothing when the tail cannot be merged, so the caller refetches", async () => {
		const before = cached();
		getMock.mockResolvedValueOnce(wire({ afterSequence: 3, turns: [wireTurn("t2", "completed")], messages: [wireMessage("m2", "t2", 4)] }));
		await expect(refreshLocalConversationTail(client, "s1")).resolves.toBe(false);
		expect(cached()).toBe(before);
	});

	it("leaves a live page that changed during the read alone", async () => {
		const edited = { ...live, settings: { model: "picked-meanwhile" } } as ConversationSnapshot;
		getMock.mockImplementationOnce(async () => {
			client.setQueryData<InfiniteData<ConversationSnapshot>>(key, (current) => current && { ...current, pages: [edited, ...current.pages.slice(1)] });
			return wire({ afterSequence: 3, oldestSequence: 4, turns: [wireTurn("t2", "running")], messages: [wireMessage("m2", "t2", 4)] });
		});
		await expect(refreshLocalConversationTail(client, "s1")).resolves.toBe(false);
		// The edit stays; the tail read before it is dropped.
		expect(cached()!.pages[0].settings).toEqual({ model: "picked-meanwhile" });
		expect(cached()!.pages[0].items.map((item) => item.id)).toEqual(live.items.map((item) => item.id));
	});

	it("reads the whole chat when a running turn or an unsettled row reaches an older loaded page", async () => {
		// t2 is long enough to span pages: its streaming command sits on the older page, below the live page's floor.
		const liveSplit = page([turn("t2", "running")], [message("r2", "t2", 5, "partial"), tool("a3", "t2", 6, "running")], { hasMoreBefore: true, oldestSequence: 5 });
		const olderSplit = page([turn("t1", "completed"), turn("t2", "running")], [message("m1", "t1", 1), message("r1", "t1", 2), message("m2", "t2", 3), tool("a2", "t2", 4, "running")]);
		client.setQueryData<InfiniteData<ConversationSnapshot>>(key, { pages: [liveSplit, olderSplit], pageParams: [undefined, 5] });
		await expect(refreshLocalConversationTail(client, "s1")).resolves.toBe(false);

		// An older page holding an unsettled row of a finished turn is not covered by the tail either.
		const liveDone = page([turn("t2", "running")], [message("m2", "t2", 4), message("r2", "t2", 5)], { hasMoreBefore: true, oldestSequence: 4 });
		const olderUnsettled = page([turn("t1", "completed")], [message("m1", "t1", 1), tool("a1", "t1", 2, "running"), message("r1", "t1", 3)]);
		client.setQueryData<InfiniteData<ConversationSnapshot>>(key, { pages: [liveDone, olderUnsettled], pageParams: [undefined, 4] });
		await expect(refreshLocalConversationTail(client, "s1")).resolves.toBe(false);
		expect(getMock).not.toHaveBeenCalled();
	});

	it("keeps a chat marked stale during the read stale, for its next open to catch up", async () => {
		const before = cached();
		getMock.mockImplementationOnce(async () => {
			// The chat closes and an event marks it stale while the tail is read.
			await client.invalidateQueries({ queryKey: key, refetchType: "none" });
			return wire({ afterSequence: 3, oldestSequence: 4, turns: [wireTurn("t2", "running")], messages: [wireMessage("m2", "t2", 4)] });
		});
		await expect(refreshLocalConversationTail(client, "s1")).resolves.toBe(false);
		expect(client.getQueryState(key)?.isInvalidated).toBe(true);
		expect(cached()).toBe(before);
	});

	it("asks nothing when no page is cached or no row is settled yet", async () => {
		client.clear();
		await expect(refreshLocalConversationTail(client, "s1")).resolves.toBe(false);
		client.setQueryData<InfiniteData<ConversationSnapshot>>(key, { pages: [page([turn("t1", "running")], [message("m1", "t1", 1)])], pageParams: [undefined] });
		await expect(refreshLocalConversationTail(client, "s1")).resolves.toBe(false);
		expect(getMock).not.toHaveBeenCalled();
	});

	it("answers false for a daemon that ignores afterSequence and for a failed read", async () => {
		const before = cached();
		getMock.mockResolvedValueOnce(wire({ oldestSequence: 4, turns: [wireTurn("t2", "running")], messages: [wireMessage("m2", "t2", 4)] }));
		await expect(refreshLocalConversationTail(client, "s1")).resolves.toBe(false);
		getMock.mockResolvedValueOnce({ error: { code: "INTERNAL" } });
		await expect(refreshLocalConversationTail(client, "s1")).resolves.toBe(false);
		expect(cached()).toBe(before);
	});
});
