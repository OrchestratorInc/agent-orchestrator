import { renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ArtifactScrollMemory, useArtifactScrollRestore } from "./artifact-scroll-memory";

/** A scroller inside a root whose content is only `box.height` tall: jsdom does not lay out. */
function scroller(height: number) {
	const root = document.createElement("div");
	const element = document.createElement("div");
	element.setAttribute("data-artifact-scroll", "");
	const box = { height };
	let top = 0;
	Object.defineProperties(element, {
		scrollTop: { get: () => top, set: (value: number) => { top = Math.min(value, box.height); } },
		scrollLeft: { get: () => 0, set: () => {} },
	});
	root.append(element);
	document.body.append(root);
	return { root, element, box };
}

describe("useArtifactScrollRestore", () => {
	let frames: FrameRequestCallback[];
	beforeEach(() => {
		frames = [];
		vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => frames.push(callback));
		vi.spyOn(window, "cancelAnimationFrame").mockImplementation(() => {});
	});
	afterEach(() => {
		vi.restoreAllMocks();
		document.body.replaceChildren();
	});

	it("scrolls back once the content is tall enough, and ignores the scrolls on the way", () => {
		const { root, element, box } = scroller(100);
		const memory = new ArtifactScrollMemory({ x: 0, y: 500 });
		renderHook(() => useArtifactScrollRestore(root, memory, true));
		expect(element.scrollTop).toBe(100);
		element.dispatchEvent(new Event("scroll"));
		expect(memory.current()).toEqual({ x: 0, y: 500 });

		box.height = 1000;
		frames.shift()!(0);
		expect(element.scrollTop).toBe(500);
		expect(memory.pendingPosition()).toBeUndefined();
		expect(frames).toHaveLength(0);
		element.scrollTop = 620;
		element.dispatchEvent(new Event("scroll"));
		expect(memory.current()).toEqual({ x: 0, y: 620 });
	});

	it("stops scrolling back once the reader takes over", () => {
		const { root, element } = scroller(100);
		const memory = new ArtifactScrollMemory({ x: 0, y: 500 });
		renderHook(() => useArtifactScrollRestore(root, memory, true));
		root.dispatchEvent(new Event("wheel"));
		expect(memory.pendingPosition()).toBeUndefined();
		element.scrollTop = 40;
		element.dispatchEvent(new Event("scroll"));
		expect(memory.current()).toEqual({ x: 0, y: 40 });
	});

	it("neither restores nor records while disabled, and forgets on clear", () => {
		const { root, element } = scroller(1000);
		const memory = new ArtifactScrollMemory({ x: 0, y: 500 });
		renderHook(() => useArtifactScrollRestore(root, memory, false));
		expect(element.scrollTop).toBe(0);
		memory.clear();
		expect(memory.current()).toBeUndefined();
		expect(memory.pendingPosition()).toBeUndefined();
	});
});
