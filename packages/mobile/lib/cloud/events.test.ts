import { describe, expect, it, vi } from "vitest";
import type { ClientEvent } from "@aoagents/cloud-client";
import { pollCloudEvents, toConversationItems } from "./events";

const userEvent: ClientEvent = {
	sessionId: "s1", sequence: 1, type: "chat.user_message",
	payload: { text: "hello" }, createdAt: "2026-09-01T00:00:00Z",
} as ClientEvent;

const deltaOne: ClientEvent = {
	sessionId: "s1", sequence: 2, type: "chat.assistant_delta",
	payload: { text: "Hi ", turnId: "t1", attempt: 1, stream: "stdout" }, createdAt: "2026-09-01T00:00:01Z",
} as ClientEvent;

const deltaTwo: ClientEvent = {
	sessionId: "s1", sequence: 3, type: "chat.assistant_delta",
	payload: { text: "there", turnId: "t1", attempt: 1, stream: "stdout" }, createdAt: "2026-09-01T00:00:02Z",
} as ClientEvent;

describe("toConversationItems", () => {
	it("maps a user message", () => {
		const [item] = toConversationItems([userEvent]);
		expect(item).toMatchObject({
			kind: "message", role: "user", origin: "human", text: "hello", sequence: 1, streaming: false,
		});
	});

	// Deltas are fragments of one reply; rendering each as its own bubble is
	// the classic streaming-chat bug.
	it("coalesces consecutive assistant deltas into one message", () => {
		const items = toConversationItems([userEvent, deltaOne, deltaTwo]);
		expect(items).toHaveLength(2);
		expect(items[1]).toMatchObject({ role: "assistant", text: "Hi there", streaming: true });
	});

	it("marks the assistant message settled once its turn completes", () => {
		const completed = {
			sessionId: "s1", sequence: 4, type: "chat.turn_completed",
			payload: { turnId: "t1", attempt: 1 }, createdAt: "2026-09-01T00:00:03Z",
		} as ClientEvent;
		const items = toConversationItems([deltaOne, completed]);
		expect(items[0]).toMatchObject({ role: "assistant", streaming: false });
	});

	it("ignores event types it has no rendering for", () => {
		const interrupt = {
			sessionId: "s1", sequence: 5, type: "chat.interrupt_requested",
			payload: { turnId: "t1" }, createdAt: "2026-09-01T00:00:04Z",
		} as ClientEvent;
		expect(toConversationItems([interrupt])).toEqual([]);
	});

	it("starts a new assistant message after an interrupted turn, rather than reopening the old one", () => {
		const interrupted = {
			sessionId: "s1", sequence: 4, type: "chat.turn_interrupted",
			payload: { turnId: "t1", attempt: 1 }, createdAt: "2026-09-01T00:00:03Z",
		} as ClientEvent;
		const deltaThree = {
			sessionId: "s1", sequence: 5, type: "chat.assistant_delta",
			payload: { text: "again", turnId: "t2", attempt: 1, stream: "stdout" }, createdAt: "2026-09-01T00:00:04Z",
		} as ClientEvent;
		const items = toConversationItems([deltaOne, interrupted, deltaThree]);
		expect(items).toHaveLength(2);
		expect(items[0]).toMatchObject({ text: "Hi ", streaming: false });
		expect(items[1]).toMatchObject({ text: "again", streaming: true });
	});
});

function makeReplayClient(pages: Array<{ events: ClientEvent[]; hasMore: boolean; nextAfter: number }>) {
	let call = 0;
	const replayEvents = vi.fn(async () => {
		const page = pages[Math.min(call, pages.length - 1)];
		call += 1;
		return page;
	});
	return { replayEvents } as unknown as import("@aoagents/cloud-client").CloudClient;
}

describe("pollCloudEvents", () => {
	it("advances the cursor monotonically and delivers new events each tick", async () => {
		vi.useFakeTimers();
		const client = makeReplayClient([
			{ events: [userEvent], hasMore: false, nextAfter: 1 },
			{ events: [deltaOne], hasMore: false, nextAfter: 2 },
			{ events: [], hasMore: false, nextAfter: 2 },
		]);
		const controller = new AbortController();
		const seen: number[] = [];
		const run = pollCloudEvents({
			client, orgId: "o1", sessionId: "s1", after: 0, signal: controller.signal, intervalMs: 10,
			onEvents: (events, cursor) => { seen.push(cursor); void events; },
		});

		await vi.advanceTimersByTimeAsync(0);
		expect(client.replayEvents).toHaveBeenNthCalledWith(1, "o1", "s1", expect.objectContaining({ after: 0 }));
		await vi.advanceTimersByTimeAsync(10);
		expect(client.replayEvents).toHaveBeenNthCalledWith(2, "o1", "s1", expect.objectContaining({ after: 1 }));
		await vi.advanceTimersByTimeAsync(10);
		expect(client.replayEvents).toHaveBeenNthCalledWith(3, "o1", "s1", expect.objectContaining({ after: 2 }));
		expect(seen).toEqual([1, 2]);

		controller.abort();
		await vi.advanceTimersByTimeAsync(10);
		await run;
		vi.useRealTimers();
	});

	it("survives a transient failure and retries on the next tick", async () => {
		vi.useFakeTimers();
		let call = 0;
		const replayEvents = vi.fn(async () => {
			call += 1;
			if (call === 1) throw new Error("network down");
			return { events: [userEvent], hasMore: false, nextAfter: 1 };
		});
		const client = { replayEvents } as unknown as import("@aoagents/cloud-client").CloudClient;
		const controller = new AbortController();
		const onEvents = vi.fn();
		const run = pollCloudEvents({
			client, orgId: "o1", sessionId: "s1", after: 0, signal: controller.signal, intervalMs: 10, onEvents,
		});

		await vi.advanceTimersByTimeAsync(0);
		expect(replayEvents).toHaveBeenCalledTimes(1);
		await vi.advanceTimersByTimeAsync(10);
		expect(replayEvents).toHaveBeenCalledTimes(2);
		expect(onEvents).toHaveBeenCalledWith([userEvent], 1);

		controller.abort();
		await vi.advanceTimersByTimeAsync(10);
		await run;
		vi.useRealTimers();
	});

	it("stops promptly on abort without leaking a pending timer", async () => {
		vi.useFakeTimers();
		const clearSpy = vi.spyOn(global, "clearTimeout");
		const client = makeReplayClient([{ events: [], hasMore: false, nextAfter: 0 }]);
		const controller = new AbortController();
		const run = pollCloudEvents({
			client, orgId: "o1", sessionId: "s1", after: 0, signal: controller.signal, intervalMs: 10_000,
			onEvents: () => {},
		});

		await vi.advanceTimersByTimeAsync(0);
		controller.abort();
		await run;
		expect(clearSpy).toHaveBeenCalled();
		clearSpy.mockRestore();
		vi.useRealTimers();
	});
});
