import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { installOverlayScrollbars } from "./overlay-scrollbars";

/** jsdom has no layout: give an element the scroll metrics of a real scroller. */
function scroller(metrics: { clientHeight: number; scrollHeight: number; scrollTop?: number }): HTMLDivElement {
	const el = document.createElement("div");
	el.style.overflowY = "auto";
	for (const [key, value] of Object.entries({ clientWidth: 200, scrollWidth: 200, scrollTop: 0, ...metrics })) {
		Object.defineProperty(el, key, { configurable: true, value, writable: true });
	}
	el.getBoundingClientRect = () => ({ left: 10, top: 20, right: 210, bottom: 20 + metrics.clientHeight, width: 200, height: metrics.clientHeight, x: 10, y: 20, toJSON: () => ({}) });
	document.body.append(el);
	return el;
}

const verticalThumb = () => document.querySelector<HTMLElement>(".overlay-scrollbar-thumb[data-axis='y']");

/** A scroll the person made: wheel input inside the element, then the scroll. */
function userScroll(el: HTMLElement) {
	el.dispatchEvent(new Event("wheel", { bubbles: true }));
	el.dispatchEvent(new Event("scroll"));
}

describe("installOverlayScrollbars", () => {
	let uninstall: () => void;

	beforeEach(() => {
		vi.useFakeTimers();
		vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => {
			callback(0);
			return 1;
		});
		uninstall = installOverlayScrollbars();
	});

	afterEach(() => {
		uninstall();
		document.body.innerHTML = "";
		vi.useRealTimers();
		vi.restoreAllMocks();
	});

	it("draws a thumb over a scrolled element without touching its layout", () => {
		const el = scroller({ clientHeight: 100, scrollHeight: 400, scrollTop: 150 });
		userScroll(el);

		const thumb = verticalThumb();
		expect(thumb?.parentElement).toHaveClass("overlay-scrollbar-layer");
		expect(thumb).toHaveAttribute("data-visible");
		// 100 / 400 of the track, offset by half the scroll range: (150 / 300) * (100 - 25).
		expect(thumb?.style.height).toBe("25px");
		expect(thumb?.style.transform).toBe("translate(204px, 57.5px)");
		expect(el.style.paddingRight).toBe("");
	});

	it("fades the thumb once scrolling stops, then lets go of the element", () => {
		const el = scroller({ clientHeight: 100, scrollHeight: 400 });
		userScroll(el);
		expect(verticalThumb()).toHaveAttribute("data-visible");

		vi.advanceTimersByTime(400);
		expect(verticalThumb()).not.toHaveAttribute("data-visible");
		// Once faded, the bar is dropped so a removed element is never retained.
		vi.advanceTimersByTime(150);
		expect(verticalThumb()).toBeNull();
	});

	it("does not reveal a thumb for a scroll the app made", () => {
		const el = scroller({ clientHeight: 100, scrollHeight: 400 });
		el.dispatchEvent(new Event("scroll"));

		expect(verticalThumb()).toBeNull();
	});

	it("does not reveal a thumb for input elsewhere", () => {
		const dialogInput = document.createElement("input");
		document.body.append(dialogInput);
		const el = scroller({ clientHeight: 100, scrollHeight: 400 });
		dialogInput.dispatchEvent(new Event("keydown", { bubbles: true }));
		el.dispatchEvent(new Event("scroll"));

		expect(verticalThumb()).toBeNull();
	});

	it("draws nothing for content that fits", () => {
		const el = scroller({ clientHeight: 100, scrollHeight: 100 });
		userScroll(el);

		expect(verticalThumb()?.style.display).toBe("none");
	});

	it("leaves the repository picker its native track", () => {
		const el = scroller({ clientHeight: 100, scrollHeight: 400 });
		el.className = "repository-picker-scrollbar";
		userScroll(el);

		expect(verticalThumb()).toBeNull();
	});

	it("leaves terminals to their own scrollbars", () => {
		const terminal = document.createElement("div");
		terminal.className = "xterm";
		document.body.append(terminal);
		const el = scroller({ clientHeight: 100, scrollHeight: 400 });
		terminal.append(el);
		userScroll(el);

		expect(verticalThumb()).toBeNull();
	});

	it("removes its layer on uninstall", () => {
		uninstall();
		expect(document.querySelector(".overlay-scrollbar-layer")).toBeNull();
		uninstall = () => {};
	});
});
