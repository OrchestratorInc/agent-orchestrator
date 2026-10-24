import { renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useScrollFollow } from "./useScrollFollow";

const motion = vi.hoisted(() => {
	vi.stubEnv("MODE", "development");
	return { reduced: true };
});
vi.mock("motion/react", () => ({ useReducedMotion: () => motion.reduced }));

let now = 100_000;
beforeEach(() => {
	now = 100_000;
	motion.reduced = true;
	vi.spyOn(performance, "now").mockImplementation(() => now);
});
afterEach(() => {
	vi.restoreAllMocks();
	vi.useRealTimers();
});

function viewport({ scrollHeight, clientHeight, scrollTop }: { scrollHeight: number; clientHeight: number; scrollTop: number }) {
	const node = document.createElement("div");
	Object.defineProperty(node, "scrollHeight", { configurable: true, value: scrollHeight });
	Object.defineProperty(node, "clientHeight", { configurable: true, value: clientHeight });
	Object.defineProperty(node, "scrollTop", { configurable: true, writable: true, value: scrollTop });
	return node;
}

describe("useScrollFollow", () => {
	it("lands on the end and recognizes the resulting scroll as its own by position", () => {
		const node = viewport({ scrollHeight: 2000, clientHeight: 500, scrollTop: 100 });
		const { result } = renderHook(() => useScrollFollow(() => node));
		// Tests snap (no layout to animate): the plain snap writes scrollHeight.
		result.current.glideToEnd();
		expect(node.scrollTop).toBe(2000);
		expect(result.current.isOwnScroll(2000)).toBe(true);
		// A reader scroll lands somewhere else, however soon after the write.
		expect(result.current.isOwnScroll(1700)).toBe(false);
	});

	it("leaves a viewport that is already at its end alone", () => {
		// The app's plain snap writes scrollHeight (the browser clamps it), so "at the end" is that value.
		const node = viewport({ scrollHeight: 2000, clientHeight: 500, scrollTop: 2000 });
		const { result } = renderHook(() => useScrollFollow(() => node));
		result.current.followEnd();
		expect(node.scrollTop).toBe(2000);
		expect(result.current.isOwnScroll(2000)).toBe(false);
	});

	it("records writes made elsewhere, such as virtualizer anchoring, as its own", () => {
		const node = viewport({ scrollHeight: 2000, clientHeight: 500, scrollTop: 0 });
		const { result } = renderHook(() => useScrollFollow(() => node));
		result.current.markWritten(640);
		expect(result.current.isOwnScroll(640)).toBe(true);
		expect(result.current.isOwnScroll(600)).toBe(false);
	});

	it("does not follow while held for reader intent, but a deliberate glide still runs", () => {
		const node = viewport({ scrollHeight: 2000, clientHeight: 500, scrollTop: 100 });
		const { result } = renderHook(() => useScrollFollow(() => node));
		result.current.hold(10_000);
		result.current.followEnd();
		expect(node.scrollTop).toBe(100);
		result.current.glideToEnd();
		expect(node.scrollTop).toBe(2000);
	});

	it("keeps repeated upward reader intent authoritative during virtualized streamed growth", () => {
		motion.reduced = false;
		const node = viewport({ scrollHeight: 4800, clientHeight: 800, scrollTop: 4000 });
		const scrollTo = vi.fn();
		node.scrollTo = scrollTo;
		const { result } = renderHook(() => useScrollFollow(() => node));

		result.current.glideToEnd();
		expect(node.scrollTo).toHaveBeenCalledWith({ top: 4000, behavior: "smooth" });
		now += 100;
		node.scrollTop = 3200;
		result.current.hold(10_000, 4000);
		result.current.hold(10_000, 3200);
		expect(result.current.isOwnScroll(3200)).toBe(false);

		Object.defineProperty(node, "scrollHeight", { configurable: true, value: 5100 });
		scrollTo.mockClear();
		result.current.followEnd();
		expect(node.scrollTop).toBe(3200);
		expect(node.scrollTo).not.toHaveBeenCalled();
	});

	it("cancels a genuine recent smooth aim before classifying repeated reader input", () => {
		motion.reduced = false;
		const node = viewport({ scrollHeight: 4000, clientHeight: 800, scrollTop: 2000 });
		let top = 2000;
		const writes = vi.fn((value: number) => { top = value; });
		Object.defineProperty(node, "scrollTop", { configurable: true, get: () => top, set: writes });
		node.scrollTo = vi.fn();
		const { result } = renderHook(() => useScrollFollow(() => node));
		result.current.glideToEnd();
		expect(node.scrollTo).toHaveBeenCalledWith({ top: 3200, behavior: "smooth" });
		now += 100;
		top = 2800;
		result.current.hold(250);
		expect(writes.mock.calls).toEqual([[2801], [2800]]);
		expect(result.current.isFlying()).toBe(false);
		top = 2400;
		result.current.hold(250);
		expect(writes.mock.calls).toEqual([[2801], [2800]]);
		expect(result.current.isOwnScroll(2400)).toBe(false);
	});

	it("keeps a genuine virtualizer adjustment app-owned after nested reader intent", () => {
		const node = viewport({ scrollHeight: 4000, clientHeight: 800, scrollTop: 3200 });
		const { result } = renderHook(() => useScrollFollow(() => node));

		result.current.hold(10_000);
		node.scrollTop = 3000;
		result.current.markWritten(3000);

		expect(result.current.isOwnScroll(3000)).toBe(true);
	});

	it.each([1, 2])("recognizes a %ipx upward step after an app write during a hold", (step) => {
		const node = viewport({ scrollHeight: 4000, clientHeight: 800, scrollTop: 3200 });
		const { result } = renderHook(() => useScrollFollow(() => node));
		result.current.hold(250);
		node.scrollTop = 3000;
		result.current.markWritten(3000);
		expect(result.current.isOwnScroll(3000)).toBe(true);
		node.scrollTop -= step;
		expect(result.current.isOwnScroll(node.scrollTop)).toBe(false);
	});

	it("skips automatic owned writes during a hold and does not claim no-ops", () => {
		const node = viewport({ scrollHeight: 4000, clientHeight: 800, scrollTop: 3200 });
		const onWrite = vi.fn();
		const { result } = renderHook(() => useScrollFollow(() => node, onWrite));
		result.current.writeOwned(node, 3200);
		expect(onWrite).not.toHaveBeenCalled();
		expect(result.current.isOwnScroll(3200)).toBe(false);
		result.current.hold(250);
		result.current.writeOwned(node, 3500);
		expect(node.scrollTop).toBe(3200);
		expect(onWrite).not.toHaveBeenCalled();
		now += 300;
		result.current.writeOwned(node, 3500);
		expect(node.scrollTop).toBe(3500);
		expect(onWrite).toHaveBeenCalledExactlyOnceWith(3500);
	});

	it("extends a deferred snap with the hold and cancels it when the reader leaves", () => {
		vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
		const node = viewport({ scrollHeight: 4000, clientHeight: 800, scrollTop: 3200 });
		const { result } = renderHook(() => useScrollFollow(() => node));
		result.current.hold(250);
		result.current.followEnd(true);
		now += 200;
		vi.advanceTimersByTime(200);
		result.current.hold(250);
		now += 50;
		vi.advanceTimersByTime(50);
		expect(node.scrollTop).toBe(3200);
		result.current.cancel();
		now += 300;
		vi.advanceTimersByTime(300);
		expect(node.scrollTop).toBe(3200);
	});

	it("does not replay a deferred snap before a queued reader scroll event", () => {
		vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
		const node = viewport({ scrollHeight: 4000, clientHeight: 800, scrollTop: 3200 });
		const { result } = renderHook(() => useScrollFollow(() => node));
		result.current.markWritten(3200);
		result.current.hold(250);
		result.current.followEnd(true);
		node.scrollTop = 3199;
		now += 300;
		vi.advanceTimersByTime(300);
		expect(node.scrollTop).toBe(3199);
		expect(result.current.isOwnScroll(3199)).toBe(false);
	});
});
