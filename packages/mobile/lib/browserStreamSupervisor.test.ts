import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { BrowserStreamSupervisor, reconnectDelayMs, STALE_AFTER_MS, type SupervisorEvents } from "./browserStreamSupervisor";
import type { BrowserTransport, BrowserTransportHandlers } from "./browserTransport";

class FakeTransport implements BrowserTransport {
	readonly capabilities = { text: true, keys: true, viewport: false, quality: false, control: false, dialogs: false };
	handlers: BrowserTransportHandlers | null = null;
	closed = false;
	connect(handlers: BrowserTransportHandlers) { this.handlers = handlers; }
	send() {}
	close() { this.closed = true; }
	open() { this.handlers?.onStatus("open"); }
	fail() { this.handlers?.onStatus("error", "gone"); }
	alive(heartbeat = true) { this.handlers?.onAlive?.({ heartbeat }); }
}

function setup() {
	const made: FakeTransport[] = [];
	let appState: ((state: string) => void) | null = null;
	let network: ((connected: boolean) => void) | null = null;
	const events: SupervisorEvents = { onStatus: vi.fn(), onState: vi.fn(), onFrame: vi.fn(), onStale: vi.fn(), onDisabled: vi.fn() };
	const supervisor = new BrowserStreamSupervisor({
		create: () => { const t = new FakeTransport(); made.push(t); return t; },
		subscribeAppState: (listener) => { appState = listener; return () => { appState = null; }; },
		subscribeNetwork: (listener) => { network = listener; return () => { network = null; }; },
	}, events);
	return { supervisor, made, events, background: () => appState?.("background"), foreground: () => appState?.("active"), network: (connected: boolean) => network?.(connected), last: () => made[made.length - 1] };
}

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe("reconnect backoff", () => {
	it("backoff schedule: 1 s doubling up to 15 s", () => {
		expect([0, 1, 2, 3, 4, 5, 9].map(reconnectDelayMs)).toEqual([1000, 2000, 4000, 8000, 15000, 15000, 15000]);
	});

	it("reconnects after an error, waiting longer each time, and resets once healthy", () => {
		const s = setup();
		s.supervisor.start();
		expect(s.made).toHaveLength(1);
		s.last().fail();
		vi.advanceTimersByTime(999);
		expect(s.made).toHaveLength(1);
		vi.advanceTimersByTime(1);
		expect(s.made).toHaveLength(2);
		// Kept a while for its probe, then closed.
		expect(s.made[0].closed).toBe(false);
		vi.advanceTimersByTime(6_000);
		expect(s.made[0].closed).toBe(true);
		s.last().fail();
		vi.advanceTimersByTime(1999);
		expect(s.made).toHaveLength(2);
		vi.advanceTimersByTime(1);
		expect(s.made).toHaveLength(3);
		// A stream that opens and proves itself alive resets the schedule.
		s.last().open();
		s.last().alive();
		s.last().fail();
		vi.advanceTimersByTime(1000);
		expect(s.made).toHaveLength(4);
		expect(s.events.onStatus).toHaveBeenCalledWith("reconnecting", "gone");
	});
});

describe("network and app state", () => {
	it("reconnect on network change: at once, not after the pending backoff", () => {
		const s = setup();
		s.supervisor.start();
		s.last().fail();
		s.last().fail();
		s.network(true);
		expect(s.made).toHaveLength(2);
		vi.advanceTimersByTime(60_000);
		// The pending backoff timer was replaced, not doubled up.
		expect(s.made).toHaveLength(2);
	});

	it("a lost network waits instead of retrying into nothing", () => {
		const s = setup();
		s.supervisor.start();
		s.network(false);
		s.last().fail();
		vi.advanceTimersByTime(60_000);
		expect(s.made).toHaveLength(1);
		s.network(true);
		expect(s.made).toHaveLength(2);
	});

	it("closes on background, schedules nothing while away, and reconnects on foreground", () => {
		const s = setup();
		s.supervisor.start();
		s.last().open();
		s.background();
		// Closed synchronously: well within the 2 s budget.
		expect(s.made[0].closed).toBe(true);
		// A late error from the closed stream does not bring it back.
		s.made[0].fail();
		s.network(true);
		vi.advanceTimersByTime(120_000);
		expect(s.made).toHaveLength(1);
		s.foreground();
		expect(s.made).toHaveLength(2);
		expect(s.made[1].closed).toBe(false);
	});

	it("stop closes the stream and every subscription", () => {
		const s = setup();
		s.supervisor.start();
		s.supervisor.stop();
		expect(s.last().closed).toBe(true);
		s.foreground();
		s.network(true);
		vi.advanceTimersByTime(60_000);
		expect(s.made).toHaveLength(1);
	});

	it("a probe slower than the first retry still stops it: the retry doesn't cancel the failed transport", () => {
		const s = setup();
		s.supervisor.start();
		const first = s.last();
		first.fail();
		vi.advanceTimersByTime(1_000);
		expect(s.made).toHaveLength(2);
		// Its socket is already dead; closing it would discard the probe.
		expect(first.closed).toBe(false);
		first.handlers?.onDisabled?.();
		expect(s.events.onDisabled).toHaveBeenCalledTimes(1);
		expect(s.made[1].closed).toBe(true);
		expect(first.closed).toBe(true);
		vi.advanceTimersByTime(60_000);
		expect(s.made).toHaveLength(2);
	});

	it("lets a failed handshake finish asking why: a late 'disabled' still stops it", () => {
		const s = setup();
		s.supervisor.start();
		const first = s.last();
		first.fail();
		// The local transport probes the route after its error, asynchronously.
		first.handlers?.onDisabled?.();
		expect(s.events.onDisabled).toHaveBeenCalledTimes(1);
		vi.advanceTimersByTime(60_000);
		expect(s.made).toHaveLength(1);
		expect(first.closed).toBe(true);
	});

	it("counts an error and the close that follows it as one failure", () => {
		const s = setup();
		s.supervisor.start();
		s.last().fail();
		s.last().handlers?.onStatus("closed");
		vi.advanceTimersByTime(1_000);
		expect(s.made).toHaveLength(2);
		vi.advanceTimersByTime(60_000);
		expect(s.made).toHaveLength(2);
	});

	it("stops for good when the host says streaming is disabled", () => {
		const s = setup();
		s.supervisor.start();
		s.last().handlers?.onDisabled?.();
		s.last().fail();
		vi.advanceTimersByTime(60_000);
		expect(s.made).toHaveLength(1);
	});
});

describe("liveness", () => {
	it("stale overlay after 3s without a heartbeat, then reconnects", () => {
		const s = setup();
		s.supervisor.start();
		s.last().open();
		s.last().alive(true);
		vi.advanceTimersByTime(STALE_AFTER_MS - 500);
		expect(s.events.onStale).not.toHaveBeenCalledWith(true);
		s.last().alive(true);
		vi.advanceTimersByTime(STALE_AFTER_MS - 500);
		expect(s.events.onStale).not.toHaveBeenCalledWith(true);
		vi.advanceTimersByTime(1000);
		expect(s.events.onStale).toHaveBeenCalledWith(true);
		expect(s.made[0].closed).toBe(true);
		vi.advanceTimersByTime(1000);
		expect(s.made).toHaveLength(2);
		s.last().open();
		s.last().alive(true);
		expect(s.events.onStale).toHaveBeenLastCalledWith(false);
	});

	it("a host that sends no heartbeat is never called stale: a still page sends nothing", () => {
		const s = setup();
		s.supervisor.start();
		s.last().open();
		s.last().alive(false);
		vi.advanceTimersByTime(60_000);
		expect(s.events.onStale).not.toHaveBeenCalledWith(true);
		expect(s.made).toHaveLength(1);
	});
});
