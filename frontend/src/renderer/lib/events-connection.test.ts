import { afterEach, expect, it, vi } from "vitest";
import { setEventsConnectionState, waitForEventsConnection } from "./events-connection";

afterEach(() => { setEventsConnectionState("idle"); vi.useRealTimers(); vi.unstubAllGlobals(); });

it("waits for the stream before the startup snapshot", async () => {
	vi.useFakeTimers();
	vi.stubGlobal("EventSource", class {});
	setEventsConnectionState("idle");
	const read = vi.fn();
	const pending = waitForEventsConnection().then(read);
	await vi.advanceTimersByTimeAsync(400);
	expect(read).not.toHaveBeenCalled();
	setEventsConnectionState("connected");
	await pending;
	expect(read).toHaveBeenCalledTimes(1);
});

it("bounds the wait when SSE is unavailable and allows later reconnect", async () => {
	vi.useFakeTimers();
	vi.stubGlobal("EventSource", class {});
	setEventsConnectionState("disconnected");
	const read = vi.fn();
	const pending = waitForEventsConnection().then(read);
	await vi.advanceTimersByTimeAsync(1_000);
	await pending;
	expect(read).toHaveBeenCalledTimes(1);
	setEventsConnectionState("connected");
	await waitForEventsConnection();
});
