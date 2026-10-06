import { renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { glideSeconds, useScrollFollow } from "./useScrollFollow";

function viewport({ scrollHeight, clientHeight, scrollTop }: { scrollHeight: number; clientHeight: number; scrollTop: number }) {
	const node = document.createElement("div");
	Object.defineProperty(node, "scrollHeight", { configurable: true, value: scrollHeight });
	Object.defineProperty(node, "clientHeight", { configurable: true, value: clientHeight });
	Object.defineProperty(node, "scrollTop", { configurable: true, writable: true, value: scrollTop });
	return node;
}

describe("glideSeconds", () => {
	it("scales with distance but stays between a quick hop and a short glide", () => {
		expect(glideSeconds(40)).toBe(0.35);
		expect(glideSeconds(800)).toBe(0.5);
		expect(glideSeconds(-800)).toBe(0.5);
		expect(glideSeconds(20_000)).toBe(0.6);
	});
});

describe("useScrollFollow", () => {
	it("lands on the end and marks the resulting scroll as its own until cancelled", () => {
		const node = viewport({ scrollHeight: 2000, clientHeight: 500, scrollTop: 100 });
		const { result } = renderHook(() => useScrollFollow(() => node));
		// jsdom has no layout to animate, so the glide snaps to the end.
		result.current.glideToEnd();
		expect(node.scrollTop).toBe(1500);
		expect(result.current.isProgrammaticScroll()).toBe(true);
		result.current.cancel();
		expect(result.current.isProgrammaticScroll()).toBe(false);
	});

	it("leaves a viewport that is already at its end alone", () => {
		const node = viewport({ scrollHeight: 2000, clientHeight: 500, scrollTop: 1500 });
		const { result } = renderHook(() => useScrollFollow(() => node));
		result.current.followEnd();
		expect(node.scrollTop).toBe(1500);
		expect(result.current.isProgrammaticScroll()).toBe(false);
	});
});
