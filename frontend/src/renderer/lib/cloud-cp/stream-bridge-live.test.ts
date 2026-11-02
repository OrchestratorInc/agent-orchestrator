import { describe, expect, it, vi } from "vitest";
import type { CloudCpStreamEvent } from "../../../main/cloud-cp-proxy";
import { subscribeSessionLiveBridged, type CloudCpStreamBridge } from "./stream-bridge";
import type { CloudCpSessionLive } from "./types";

function fakeBridge() {
	let listener: ((event: CloudCpStreamEvent) => void) | undefined;
	const bridge: CloudCpStreamBridge & { emit: (event: CloudCpStreamEvent) => void } = {
		openStream: vi.fn(async () => ({ streamId: "stream-1" })),
		closeStream: vi.fn(),
		onStreamEvent: vi.fn((_id, next) => {
			listener = next;
			return () => { listener = undefined; };
		}),
		emit: (event) => listener?.(event),
	};
	return bridge;
}

const frame: CloudCpSessionLive = {
	type: "session.live",
	viewers: [{ userId: "u1", name: "Alice", role: "member" }],
	composer: { holder: null, epoch: 0, waitlist: [] },
	canTakeOver: true,
	you: "u1",
	chatSettings: { model: "opus" },
	canConfigure: true,
};

describe("subscribeSessionLiveBridged", () => {
	it("asks for live frames only and delivers each session.live frame", async () => {
		const bridge = fakeBridge();
		const received: CloudCpSessionLive[] = [];
		const done = subscribeSessionLiveBridged({
			baseUrl: "https://cp.test/", orgId: "org 1", sessionId: "session-1", bridge,
			onLive: (live) => received.push(live),
		});
		await vi.waitFor(() => expect(bridge.onStreamEvent).toHaveBeenCalled());
		expect(bridge.openStream).toHaveBeenCalledWith(expect.objectContaining({
			baseUrl: "https://cp.test",
			path: "/api/cloud/v1/orgs/org%201/sessions/session-1/events?live=1&types=session.live",
			headers: { Accept: "text/event-stream" },
		}));
		bridge.emit({ type: "chunk", data: `retry: 2000\n\nevent: session.live\ndata: ${JSON.stringify(frame)}\n\n: keepalive\n\n` });
		// An ordinary event on the same stream is not live state.
		bridge.emit({ type: "chunk", data: `id: 4\nevent: chat.user_message\ndata: {"type":"chat.user_message"}\n\n` });
		bridge.emit({ type: "end" });
		await done;
		expect(received).toEqual([frame]);
	});

	it("closes the stream when aborted", async () => {
		const bridge = fakeBridge();
		const controller = new AbortController();
		const done = subscribeSessionLiveBridged({
			baseUrl: "https://cp.test", orgId: "org-1", sessionId: "session-1", bridge, signal: controller.signal, onLive: vi.fn(),
		});
		await vi.waitFor(() => expect(bridge.onStreamEvent).toHaveBeenCalled());
		controller.abort();
		await done;
		expect(bridge.closeStream).toHaveBeenCalledWith("stream-1");
	});

	it("reports a malformed frame", async () => {
		const bridge = fakeBridge();
		const onError = vi.fn();
		const done = subscribeSessionLiveBridged({
			baseUrl: "https://cp.test", orgId: "org-1", sessionId: "session-1", bridge, onLive: vi.fn(), onError,
		});
		await vi.waitFor(() => expect(bridge.onStreamEvent).toHaveBeenCalled());
		bridge.emit({ type: "chunk", data: "event: session.live\ndata: {not json\n\n" });
		bridge.emit({ type: "end" });
		await done;
		expect(onError).toHaveBeenCalledTimes(1);
	});
});
