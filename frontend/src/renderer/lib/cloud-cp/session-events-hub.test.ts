import { afterEach, describe, expect, it, vi } from "vitest";
import type { CloudCpStreamEvent } from "../../../main/cloud-cp-proxy";
import type { CloudCpStreamBridge } from "./stream-bridge";
import { cloudSessionEventsState, subscribeCloudSessionEvents, type CloudSessionEventsSubscriber } from "./session-events-hub";
import type { CloudCpClientEvent, CloudCpSessionLive } from "./types";

const TARGET = { baseUrl: "https://cp.test/", orgId: "org-1", sessionId: "session-1" };
const EVENTS_PATH = "/api/cloud/v1/orgs/org-1/sessions/session-1/events";

function fakeBridge() {
	const listeners = new Map<string, (event: CloudCpStreamEvent) => void>();
	let next = 0;
	const bridge = {
		openStream: vi.fn(async (_init: { path: string }) => ({ streamId: `stream-${++next}` })),
		closeStream: vi.fn((streamId: string) => listeners.delete(streamId)),
		onStreamEvent: vi.fn((streamId: string, listener: (event: CloudCpStreamEvent) => void) => {
			listeners.set(streamId, listener);
			return () => listeners.delete(streamId);
		}),
		emit: (streamId: string, event: CloudCpStreamEvent) => listeners.get(streamId)?.(event),
		paths: () => bridge.openStream.mock.calls.map(([init]) => init.path),
	};
	return bridge satisfies CloudCpStreamBridge;
}

function eventFrame(sequence: number, type = "chat.assistant_delta"): CloudCpStreamEvent {
	const event: CloudCpClientEvent = { sessionId: "session-1", sequence, type, payload: {}, createdAt: "2026-11-01T00:00:00Z" };
	return { type: "chunk", data: `id: ${sequence}\nevent: ${type}\ndata: ${JSON.stringify(event)}\n\n` };
}

const liveState: CloudCpSessionLive = {
	type: "session.live", viewers: [], composer: { holder: null, epoch: 0, waitlist: [] },
	canTakeOver: false, you: "user-1", chatSettings: {}, canConfigure: true,
};

const cleanups: Array<() => void> = [];
function subscribe(bridge: ReturnType<typeof fakeBridge>, subscriber: CloudSessionEventsSubscriber, target = TARGET) {
	const unsubscribe = subscribeCloudSessionEvents(target, subscriber, { bridge });
	cleanups.push(unsubscribe);
	return unsubscribe;
}

afterEach(() => {
	for (const cleanup of cleanups.splice(0)) cleanup();
	vi.useRealTimers();
});

describe("subscribeCloudSessionEvents", () => {
	it("opens one stream for everything a session view follows, and closes it with the last", async () => {
		const bridge = fakeBridge();
		const chat = vi.fn();
		const pullRequests = vi.fn();
		const setupLog = vi.fn();
		// The chat (resuming after what its poll loaded), the header's live
		// state, the inspector's pull requests and Setup log, all mounted at once.
		const leave = [
			subscribe(bridge, { onEvent: chat, resumeAfter: () => 40 }),
			subscribe(bridge, { live: true }),
			subscribe(bridge, { onEvent: pullRequests }),
			subscribe(bridge, { onEvent: setupLog, resumeAfter: () => 42 }),
		];
		await vi.waitFor(() => expect(bridge.onStreamEvent).toHaveBeenCalledTimes(1));
		expect(bridge.paths()).toEqual([`${EVENTS_PATH}?after=40&live=1`]);

		bridge.emit("stream-1", eventFrame(41));
		for (const listener of [chat, pullRequests, setupLog]) expect(listener).toHaveBeenCalledWith(expect.objectContaining({ sequence: 41 }));

		// The chat closing keeps the stream for the rest.
		leave[0]();
		leave[2]();
		await new Promise((resolve) => setTimeout(resolve, 5));
		expect(bridge.closeStream).not.toHaveBeenCalled();
		// Leaving the session view closes it, without opening another.
		leave[1]();
		leave[3]();
		await new Promise((resolve) => setTimeout(resolve, 5));
		expect(bridge.closeStream).toHaveBeenCalledTimes(1);
		expect(bridge.closeStream).toHaveBeenCalledWith("stream-1");
		expect(bridge.openStream).toHaveBeenCalledTimes(1);
	});

	it("is up as soon as the stream opens, before any event, and down when it drops", async () => {
		vi.useFakeTimers();
		const bridge = fakeBridge();
		const states: boolean[] = [];
		subscribe(bridge, { onEvent: vi.fn(), resumeAfter: () => 7, onStateChange: (state) => states.push(state.open) });
		await vi.advanceTimersByTimeAsync(0);
		expect(bridge.onStreamEvent).toHaveBeenCalledTimes(1);
		expect(states).toEqual([true]);
		expect(cloudSessionEventsState(TARGET).open).toBe(true);

		bridge.emit("stream-1", { type: "end" });
		await vi.advanceTimersByTimeAsync(0);
		expect(states).toEqual([true, false]);
		// It reconnects by itself, from where it was.
		await vi.advanceTimersByTimeAsync(15_000);
		expect(bridge.paths().at(-1)).toBe(`${EVENTS_PATH}?after=7`);
		expect(states.at(-1)).toBe(true);
	});

	it("carries live state on the same stream", async () => {
		const bridge = fakeBridge();
		const onStateChange = vi.fn();
		subscribe(bridge, { live: true, onStateChange });
		subscribe(bridge, { onEvent: vi.fn(), resumeAfter: () => 3 });
		await vi.waitFor(() => expect(bridge.onStreamEvent).toHaveBeenCalledTimes(1));
		bridge.emit("stream-1", { type: "chunk", data: `event: session.live\ndata: ${JSON.stringify(liveState)}\n\n` });
		expect(cloudSessionEventsState(TARGET).live).toEqual(liveState);
		expect(onStateChange).toHaveBeenLastCalledWith({ open: true, live: liveState });
	});

	it("waits for a subscriber still loading what it holds, then resumes after it", async () => {
		vi.useFakeTimers();
		const bridge = fakeBridge();
		let loaded: number | undefined;
		subscribe(bridge, { onEvent: vi.fn(), resumeAfter: () => loaded ?? "loading" });
		subscribe(bridge, { live: true });
		await vi.advanceTimersByTimeAsync(600);
		expect(bridge.openStream).not.toHaveBeenCalled();
		loaded = 120;
		await vi.advanceTimersByTimeAsync(300);
		expect(bridge.paths()).toEqual([`${EVENTS_PATH}?after=120&live=1`]);
	});

	it("resumes after the newest event it delivered when the stream drops", async () => {
		vi.useFakeTimers();
		const bridge = fakeBridge();
		subscribe(bridge, { onEvent: vi.fn(), resumeAfter: () => 5 });
		await vi.advanceTimersByTimeAsync(0);
		bridge.emit("stream-1", eventFrame(6));
		bridge.emit("stream-1", eventFrame(9));
		bridge.emit("stream-1", { type: "end" });
		await vi.advanceTimersByTimeAsync(1_000);
		expect(bridge.paths()).toEqual([`${EVENTS_PATH}?after=5`, `${EVENTS_PATH}?after=9`]);
	});

	it("asks a live-only stream for no events, and reopens it with events when a listener joins", async () => {
		const bridge = fakeBridge();
		subscribe(bridge, { live: true });
		await vi.waitFor(() => expect(bridge.onStreamEvent).toHaveBeenCalledTimes(1));
		expect(bridge.paths()).toEqual([`${EVENTS_PATH}?live=1&types=session.live`]);

		const leave = subscribe(bridge, { onEvent: vi.fn(), resumeAfter: () => 11 });
		await vi.waitFor(() => expect(bridge.onStreamEvent).toHaveBeenCalledTimes(2));
		expect(bridge.closeStream).toHaveBeenCalledWith("stream-1");
		expect(bridge.paths()[1]).toBe(`${EVENTS_PATH}?after=11&live=1`);

		// Back to live state only once the listener leaves.
		leave();
		await vi.waitFor(() => expect(bridge.onStreamEvent).toHaveBeenCalledTimes(3));
		expect(bridge.paths()[2]).toBe(`${EVENTS_PATH}?live=1&types=session.live`);
	});

	it("reopens from an older point for a listener that needs events the stream started after", async () => {
		const bridge = fakeBridge();
		subscribe(bridge, { onEvent: vi.fn(), resumeAfter: () => 50 });
		await vi.waitFor(() => expect(bridge.onStreamEvent).toHaveBeenCalledTimes(1));
		const loader = vi.fn();
		subscribe(bridge, { onEvent: loader, resumeAfter: () => 0 });
		await vi.waitFor(() => expect(bridge.onStreamEvent).toHaveBeenCalledTimes(2));
		expect(bridge.paths()[1]).toBe(`${EVENTS_PATH}?after=0`);
		// One that needs nothing older joins without reopening it.
		subscribe(bridge, { onEvent: vi.fn(), resumeAfter: () => 60 });
		await new Promise((resolve) => setTimeout(resolve, 5));
		expect(bridge.openStream).toHaveBeenCalledTimes(2);
	});

	it("keeps sessions apart", async () => {
		const bridge = fakeBridge();
		subscribe(bridge, { onEvent: vi.fn() });
		subscribe(bridge, { onEvent: vi.fn() }, { ...TARGET, sessionId: "session-2" });
		await vi.waitFor(() => expect(bridge.onStreamEvent).toHaveBeenCalledTimes(2));
		expect(bridge.paths()).toEqual([EVENTS_PATH, "/api/cloud/v1/orgs/org-1/sessions/session-2/events"]);
	});
});
