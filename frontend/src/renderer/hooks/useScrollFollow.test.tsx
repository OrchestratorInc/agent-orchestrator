import { renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
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

// Outside test mode the hook animates with the browser's smooth scroll: these stub
// the mode and record the smooth scrolls it asks for.
describe("useScrollFollow when it can animate", () => {
	beforeEach(() => vi.stubEnv("MODE", "production"));
	afterEach(() => vi.unstubAllEnvs());

	function animated(geometry: { scrollHeight: number; clientHeight: number; scrollTop: number }) {
		const node = viewport(geometry);
		const scrollTo = vi.fn();
		node.scrollTo = scrollTo as typeof node.scrollTo;
		const resize = (scrollHeight: number) => Object.defineProperty(node, "scrollHeight", { configurable: true, value: scrollHeight });
		return { node, scrollTo, resize };
	}

	it("glides a short way to the end", () => {
		const { node, scrollTo } = animated({ scrollHeight: 2000, clientHeight: 500, scrollTop: 1200 });
		const { result } = renderHook(() => useScrollFollow(() => node));
		result.current.glideToEnd();
		expect(scrollTo).toHaveBeenCalledWith({ top: 1500, behavior: "smooth" });
		expect(node.scrollTop).toBe(1200);
		expect(result.current.isGliding()).toBe(true);
	});

	// A send or Jump to latest from far up used to ride smoothly through every screen.
	it("cuts to a screen short of a far end before gliding the rest", () => {
		const { node, scrollTo } = animated({ scrollHeight: 5000, clientHeight: 500, scrollTop: 100 });
		const { result } = renderHook(() => useScrollFollow(() => node));
		result.current.glideToEnd();
		expect(node.scrollTop).toBe(4000);
		expect(scrollTo).toHaveBeenCalledWith({ top: 4500, behavior: "smooth" });
		expect(result.current.isOwnScroll(4000)).toBe(true);
		expect(result.current.isOwnScroll(4300)).toBe(true);
	});

	it("lands a follow more than a screen from the end at once", () => {
		const { node, scrollTo } = animated({ scrollHeight: 5000, clientHeight: 500, scrollTop: 100 });
		const { result } = renderHook(() => useScrollFollow(() => node));
		result.current.followEnd();
		expect(node.scrollTop).toBe(4500);
		expect(scrollTo).not.toHaveBeenCalled();
		expect(result.current.isFlying()).toBe(false);
	});

	// Rows loading above the view in mid-glide used to leave it showing an earlier
	// part of the chat, then ride back down from there.
	it("moves the view and the glide in flight together when rows above change", () => {
		const { node, scrollTo, resize } = animated({ scrollHeight: 2000, clientHeight: 500, scrollTop: 1200 });
		const { result } = renderHook(() => useScrollFollow(() => node));
		result.current.glideToEnd();
		resize(2300);
		result.current.shift(300);
		expect(node.scrollTop).toBe(1500);
		expect(scrollTo).toHaveBeenLastCalledWith({ top: 1800, behavior: "smooth" });
		expect(result.current.isGliding()).toBe(true);
		expect(result.current.isOwnScroll(1650)).toBe(true);
	});

	it("ends the glide when a shift puts the view on the end", () => {
		const { node, scrollTo, resize } = animated({ scrollHeight: 2000, clientHeight: 500, scrollTop: 1200 });
		const { result } = renderHook(() => useScrollFollow(() => node));
		result.current.glideToEnd();
		// The glide reached the end; then a row above grew.
		node.scrollTop = 1500;
		resize(2300);
		result.current.shift(300);
		expect(node.scrollTop).toBe(1800);
		expect(scrollTo).toHaveBeenCalledTimes(1);
		expect(result.current.isFlying()).toBe(false);
	});

	it("only moves the view when nothing is in flight", () => {
		const { node, scrollTo } = animated({ scrollHeight: 4000, clientHeight: 500, scrollTop: 1000 });
		const { result } = renderHook(() => useScrollFollow(() => node));
		result.current.shift(-250);
		expect(node.scrollTop).toBe(750);
		expect(scrollTo).not.toHaveBeenCalled();
		expect(result.current.isOwnScroll(750)).toBe(true);
	});
});
