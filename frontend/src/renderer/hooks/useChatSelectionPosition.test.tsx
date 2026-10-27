import { act, fireEvent, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useChatSelectionPosition } from "./useChatSelectionPosition";

afterEach(() => {
	window.getSelection()?.removeAllRanges();
	document.body.replaceChildren();
	vi.restoreAllMocks();
});

function setup() {
	const viewport = document.createElement("div");
	const source = document.createElement("div");
	source.dataset.chatMessageId = "source";
	source.textContent = "Exact selected text";
	viewport.append(source);
	const button = document.createElement("div");
	document.body.append(viewport, button);
	let selectionTop = 200;
	let selectionHeight = 40;
	let selectionLeft = 200;
	let viewportWidth = 500;
	const rect = (x: number, y: number, width: number, height: number) => new DOMRect(x, y, width, height);
	vi.spyOn(viewport, "getBoundingClientRect").mockImplementation(() => rect(0, 100, viewportWidth, 400));
	vi.spyOn(button, "getBoundingClientRect").mockImplementation(() => rect(0, 0, 120, 36));
	const range = document.createRange();
	range.selectNodeContents(source);
	Object.defineProperty(range, "getBoundingClientRect", { value: vi.fn(() => rect(selectionLeft, selectionTop, 80, selectionHeight)) });
	window.getSelection()?.addRange(range);
	const invalid = vi.fn();
	let scheduled: FrameRequestCallback | undefined;
	vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => { scheduled = callback; return 1; });
	const hook = renderHook(() => useChatSelectionPosition(range, { current: viewport }, { current: button }, invalid));
	return {
		...hook, viewport, source, invalid, range,
		tall() { selectionHeight = 1000; selectionTop = -200; },
		move(top: number) { selectionTop = top; },
		narrow() { viewportWidth = 160; selectionLeft = 140; },
		flush() { act(() => { const callback = scheduled; scheduled = undefined; callback?.(0); }); },
	};
}

describe("selection action positioning", () => {
	it("follows a short selection, hides offscreen, and returns without writing scrollTop", () => {
		const h = setup();
		expect(h.result.current).toEqual({ left: 180, top: 58, visible: true });
		h.viewport.scrollTop = 77;
		h.move(160);
		fireEvent.scroll(h.viewport);
		h.flush();
		expect(h.result.current?.top).toBe(18);
		expect(h.viewport.scrollTop).toBe(77);
		h.move(60);
		fireEvent.scroll(h.viewport);
		h.flush();
		expect(h.result.current?.visible).toBe(false);
		h.move(250);
		fireEvent.scroll(h.viewport);
		h.flush();
		expect(h.result.current?.visible).toBe(true);
	});

	it("sticks a tall selection to the visible pane top until the entire range leaves", () => {
		const h = setup(); h.tall();
		fireEvent.scroll(h.viewport); h.flush();
		expect(h.result.current).toMatchObject({ top: 8, visible: true });
		h.move(-1200); fireEvent.scroll(h.viewport); h.flush();
		expect(h.result.current?.visible).toBe(false);
		h.move(-500); fireEvent.scroll(h.viewport); h.flush();
		expect(h.result.current).toMatchObject({ top: 8, visible: true });
	});

	it("coalesces a burst of geometry events into one read without reading selected text", () => {
		const h = setup();
		const bounds = vi.mocked(h.range.getBoundingClientRect); bounds.mockClear();
		const text = vi.spyOn(h.range, "toString");
		for (let i = 0; i < 100; i++) fireEvent.scroll(h.viewport);
		expect(bounds).not.toHaveBeenCalled(); h.flush();
		expect(bounds).toHaveBeenCalledTimes(1);
		expect(text).not.toHaveBeenCalled();
		h.unmount();
	});

	it("clamps using measured button width after a resize", () => {
		const h = setup();
		h.narrow();
		fireEvent(window, new Event("resize"));
		h.flush();
		expect(h.result.current?.left).toBe(32);
	});

	it("clears an action when selection changes or the source disappears", () => {
		const h = setup();
		window.getSelection()?.removeAllRanges();
		fireEvent(document, new Event("selectionchange"));
		h.flush();
		expect(h.invalid).toHaveBeenCalled();
		h.invalid.mockClear();
		h.source.remove();
		fireEvent.scroll(h.viewport);
		h.flush();
		expect(h.invalid).toHaveBeenCalled();
	});
});
