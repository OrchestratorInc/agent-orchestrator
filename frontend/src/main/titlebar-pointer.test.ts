import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { startTitlebarPointerTracking, TITLEBAR_POINTER_CHANNEL } from "./titlebar-pointer";

function setup(overrides: { focused?: boolean; zoom?: number } = {}) {
	let point = { x: 0, y: 0 };
	const send = vi.fn();
	const window = {
		isDestroyed: () => false,
		isFocused: () => overrides.focused ?? true,
		getContentBounds: () => ({ x: 100, y: 200, width: 800, height: 600 }),
	};
	const shell = { isDestroyed: () => false, getZoomFactor: () => overrides.zoom ?? 1, send };
	const stop = startTitlebarPointerTracking({
		window,
		shell: () => shell,
		getCursorScreenPoint: () => point,
		intervalMs: 10,
	});
	return {
		send,
		stop,
		move: (x: number, y: number) => {
			point = { x, y };
			vi.advanceTimersByTime(10);
		},
	};
}

describe("startTitlebarPointerTracking", () => {
	beforeEach(() => vi.useFakeTimers());
	afterEach(() => vi.useRealTimers());

	it("reports the cursor x in CSS px while inside the titlebar band", () => {
		const t = setup();
		t.move(250, 210);
		expect(t.send).toHaveBeenCalledWith(TITLEBAR_POINTER_CHANNEL, 150);
		t.stop();
	});

	it("reports null once the cursor drops below the band or leaves the window", () => {
		const t = setup();
		t.move(250, 210);
		t.move(250, 260);
		expect(t.send).toHaveBeenLastCalledWith(TITLEBAR_POINTER_CHANNEL, null);
		t.move(250, 210);
		t.move(50, 210);
		expect(t.send).toHaveBeenLastCalledWith(TITLEBAR_POINTER_CHANNEL, null);
		t.stop();
	});

	it("only sends when the value changes", () => {
		const t = setup();
		t.move(250, 210);
		t.move(250, 210);
		t.move(250, 210);
		expect(t.send).toHaveBeenCalledTimes(1);
		t.stop();
	});

	it("scales by window zoom", () => {
		const t = setup({ zoom: 2 });
		t.move(300, 210);
		expect(t.send).toHaveBeenCalledWith(TITLEBAR_POINTER_CHANNEL, 100);
		// The band is 36 CSS px tall = 72 points at 2x zoom.
		t.move(300, 270);
		expect(t.send).toHaveBeenLastCalledWith(TITLEBAR_POINTER_CHANNEL, 100);
		t.move(300, 280);
		expect(t.send).toHaveBeenLastCalledWith(TITLEBAR_POINTER_CHANNEL, null);
		t.stop();
	});

	it("reports nothing while the window is not focused", () => {
		const t = setup({ focused: false });
		t.move(250, 210);
		expect(t.send).not.toHaveBeenCalledWith(TITLEBAR_POINTER_CHANNEL, 150);
		t.stop();
	});

	it("stops polling when stopped", () => {
		const t = setup();
		t.stop();
		t.move(250, 210);
		expect(t.send).not.toHaveBeenCalled();
	});
});
