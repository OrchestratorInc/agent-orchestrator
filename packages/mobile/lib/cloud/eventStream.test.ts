import { afterEach, describe, expect, it, vi } from "vitest";
import { mergeCloudEvents } from "./conversation";
import { CloudError } from "./errors";
import { STREAM_FLUSH_MS, followCloudSessionEvents, parseCloudSseFrame, reconnectDelay } from "./eventStream";
import type { CloudClientEvent } from "./types";

const event = (sequence: number, sessionId = "s1"): CloudClientEvent => ({ sessionId, sequence, type: "chat.assistant_delta", payload: { turnId: "t1" }, createdAt: "2026-11-05T00:00:00Z" });
const frame = (value: CloudClientEvent) => `id: ${value.sequence}\nevent: ${value.type}\ndata: ${JSON.stringify(value)}\n\n`;

/** A response whose body yields each chunk, then stays open until aborted (or ends, when `end` is set). */
function streamResponse(chunks: string[], signal: AbortSignal, end = false): Response {
	const encoder = new TextEncoder();
	const body = new ReadableStream<Uint8Array>({
		start(controller) {
			for (const chunk of chunks) controller.enqueue(encoder.encode(chunk));
			if (end) controller.close();
			else signal.addEventListener("abort", () => { try { controller.error(new Error("aborted")); } catch { /* closed */ } });
		},
	});
	return new Response(body, { status: 200, headers: { "Content-Type": "text/event-stream" } });
}

afterEach(() => vi.useRealTimers());

describe("cloud session event stream", () => {
	it("reads client events and skips keep-alives and live-state frames", () => {
		expect(parseCloudSseFrame(frame(event(7)).trim())).toMatchObject({ sequence: 7, type: "chat.assistant_delta" });
		expect(parseCloudSseFrame(": keepalive")).toBeUndefined();
		expect(parseCloudSseFrame(`event: session.live\ndata: {"viewers":[]}`)).toBeUndefined();
		expect(parseCloudSseFrame("event: chat.activity\ndata: {not json")).toBeUndefined();
		expect(parseCloudSseFrame(`event: x\ndata: {"type":"x"}`)).toBeUndefined();
		expect(parseCloudSseFrame(`event: chat.activity\r\ndata:{"sessionId":"s1","sequence":3,"type":"chat.activity","payload":{},"createdAt":""}`)).toMatchObject({ sequence: 3 });
	});

	it("reconnects soon after a healthy stream and backs off after a refused one", () => {
		expect(reconnectDelay(1_000, true)).toBe(1_000);
		expect(reconnectDelay(8_000, true)).toBe(1_000);
		expect(reconnectDelay(1_000, false)).toBe(2_000);
		expect(reconnectDelay(2_000, false)).toBe(4_000);
		expect(reconnectDelay(15_000, false)).toBe(15_000);
	});

	it("resumes after the newest event held, delivers a burst at once, and says when it is open", async () => {
		const opens: number[] = [];
		const delivered: number[][] = [];
		const open = vi.fn();
		const client = {
			openEventStream: vi.fn(async (_org: string, _session: string, input: { after: number; signal: AbortSignal }) => {
				opens.push(input.after);
				// Split one frame across chunks, as a network read can.
				const second = frame(event(12));
				return streamResponse([frame(event(11)), second.slice(0, 20), second.slice(20), frame(event(99, "other"))], input.signal);
			}),
		};
		const stop = followCloudSessionEvents({ client, orgId: "o", sessionId: "s1", resumeAfter: () => 10, onEvents: (events) => delivered.push(events.map((item) => item.sequence)), onOpenChange: open });
		await vi.waitFor(() => expect(delivered).toEqual([[11, 12]]), { timeout: STREAM_FLUSH_MS * 20 });
		expect(opens).toEqual([10]);
		expect(open).toHaveBeenCalledWith(true);
		stop();
	});

	it("reconnects from what the chat holds when the stream ends", async () => {
		vi.useFakeTimers();
		let held = 5;
		const opens: number[] = [];
		const open = vi.fn();
		const client = {
			openEventStream: vi.fn(async (_org: string, _session: string, input: { after: number; signal: AbortSignal }) => {
				opens.push(input.after);
				return streamResponse(opens.length === 1 ? [frame(event(6))] : [], input.signal, opens.length === 1);
			}),
		};
		const stop = followCloudSessionEvents({ client, orgId: "o", sessionId: "s1", resumeAfter: () => held, onEvents: (events) => { held = events.at(-1)!.sequence; }, onOpenChange: open });
		await vi.advanceTimersByTimeAsync(STREAM_FLUSH_MS);
		expect(open.mock.calls.map((call) => call[0])).toEqual([true, false]);
		await vi.advanceTimersByTimeAsync(1_000);
		expect(opens).toEqual([5, 6]);
		stop();
	});

	it("stops for good when the control plane has no stream for the session", async () => {
		vi.useFakeTimers();
		const client = { openEventStream: vi.fn(async () => { throw new CloudError("Not found", { status: 404, code: "not_found" }); }) };
		const stop = followCloudSessionEvents({ client, orgId: "o", sessionId: "s1", resumeAfter: () => 0, onEvents: vi.fn(), onOpenChange: vi.fn() });
		await vi.advanceTimersByTimeAsync(60_000);
		expect(client.openEventStream).toHaveBeenCalledTimes(1);
		stop();
	});

	it("keeps retrying, with backoff, while the network is down", async () => {
		vi.useFakeTimers();
		const client = { openEventStream: vi.fn(async () => { throw new CloudError("Couldn't reach Orchestrator Cloud.", { status: 0 }); }) };
		const stop = followCloudSessionEvents({ client, orgId: "o", sessionId: "s1", resumeAfter: () => 0, onEvents: vi.fn(), onOpenChange: vi.fn() });
		await vi.advanceTimersByTimeAsync(2_000 + 4_000 + 10);
		expect(client.openEventStream).toHaveBeenCalledTimes(3);
		stop();
		await vi.advanceTimersByTimeAsync(60_000);
		expect(client.openEventStream).toHaveBeenCalledTimes(3);
	});

	it("merges the poll and the stream without dropping either's events", () => {
		const held = [event(1), event(2)];
		expect(mergeCloudEvents(held, [event(3), event(4)]).map((item) => item.sequence)).toEqual([1, 2, 3, 4]);
		const streamed = [event(1), event(2), event(5), event(6)];
		expect(mergeCloudEvents(streamed, [event(3), event(4), event(5)]).map((item) => item.sequence)).toEqual([1, 2, 3, 4, 5, 6]);
		expect(mergeCloudEvents(held, [event(2)])).toBe(held);
	});
});
