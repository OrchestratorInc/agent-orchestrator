import { renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { useScrollFollow } from "./useScrollFollow";

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
});
