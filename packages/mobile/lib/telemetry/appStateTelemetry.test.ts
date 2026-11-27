import { describe, expect, it, vi } from "vitest";
import { createAppStateTelemetry } from "./appStateTelemetry";
import type { UiUsageTracker } from "./uiUsage";
import { reportRequestTimed, requestStartedIn, setUiUsageTracker } from "./uiUsageRegistry";

function setup(options: { initial?: string; paired?: boolean; enabled?: boolean } = {}) {
	let enabled = options.enabled ?? true;
	const listeners = new Set<(on: boolean) => void>();
	const tracker = {
		setForeground: vi.fn(),
		resumed: vi.fn(),
		requestTimed: vi.fn(),
		isEnabled: () => enabled,
		onEnabledChange: (listener: (on: boolean) => void) => {
			listeners.add(listener);
			return () => listeners.delete(listener);
		},
	};
	const stalls = { start: vi.fn(), stop: vi.fn() };
	const onActive = vi.fn();
	const onBackground = vi.fn();
	const app = createAppStateTelemetry({
		initial: options.initial ?? "active",
		tracker: () => tracker as unknown as UiUsageTracker,
		paired: () => options.paired ?? true,
		stalls,
		onActive,
		onBackground,
	});
	const setEnabled = (on: boolean) => {
		enabled = on;
		for (const listener of listeners) listener(on);
	};
	return { app, tracker, stalls, onActive, onBackground, setEnabled, listeners };
}

describe("returns to the app", () => {
	it("times a return only after a real background", () => {
		const t = setup();
		t.app.change("background");
		t.app.change("active");
		expect(t.tracker.resumed).toHaveBeenCalledTimes(1);
		expect(t.tracker.setForeground.mock.calls).toEqual([[false], [true]]);
		expect(t.onBackground).toHaveBeenCalledTimes(1);
	});

	// Control Center, Notification Center, a peek at the switcher, Face ID.
	it("does not count coming back from inactive as a return", () => {
		const t = setup();
		t.app.change("inactive");
		t.app.change("active");
		expect(t.tracker.resumed).not.toHaveBeenCalled();
		expect(t.tracker.setForeground).not.toHaveBeenCalledWith(false);
	});

	it("counts a background reached through inactive once", () => {
		const t = setup();
		t.app.change("inactive");
		t.app.change("background");
		t.app.change("inactive");
		t.app.change("active");
		t.app.change("inactive");
		t.app.change("active");
		expect(t.tracker.resumed).toHaveBeenCalledTimes(1);
	});

	it("does not time a return with no machine paired", () => {
		const t = setup({ paired: false });
		t.app.change("background");
		t.app.change("active");
		expect(t.tracker.resumed).not.toHaveBeenCalled();
		expect(t.onActive).toHaveBeenCalledTimes(1);
	});
});

describe("stall probe", () => {
	it("runs only while in front", () => {
		const t = setup();
		expect(t.stalls.start).toHaveBeenCalledTimes(1);
		t.app.change("inactive");
		expect(t.stalls.stop).toHaveBeenCalledTimes(1);
		t.app.change("active");
		expect(t.stalls.start).toHaveBeenCalledTimes(2);
	});

	it("does not run while telemetry is off, and follows the switch", () => {
		const t = setup({ enabled: false });
		expect(t.stalls.start).not.toHaveBeenCalled();
		t.app.change("active");
		expect(t.stalls.start).not.toHaveBeenCalled();
		t.setEnabled(true);
		expect(t.stalls.start).toHaveBeenCalledTimes(1);
		t.setEnabled(false);
		expect(t.stalls.stop).toHaveBeenCalled();
	});

	it("stops and unsubscribes on dispose", () => {
		const t = setup();
		t.app.dispose();
		expect(t.stalls.stop).toHaveBeenCalled();
		expect(t.listeners.size).toBe(0);
	});
});

describe("requests across a background", () => {
	it("are not timed", () => {
		const t = setup();
		setUiUsageTracker(t.tracker as unknown as UiUsageTracker);
		try {
			const before = requestStartedIn();
			t.app.change("background");
			reportRequestTimed(40_000, "lan", true, before);
			reportRequestTimed(null, "lan", false, before);
			expect(t.tracker.requestTimed).not.toHaveBeenCalled();
			t.app.change("active");
			reportRequestTimed(120, "lan", true, requestStartedIn());
			expect(t.tracker.requestTimed).toHaveBeenCalledWith(120, "lan", true);
		} finally {
			setUiUsageTracker(null);
		}
	});
});
