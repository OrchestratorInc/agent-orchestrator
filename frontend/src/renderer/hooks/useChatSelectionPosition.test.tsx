import { act, fireEvent, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useChatSelectionPosition } from "./useChatSelectionPosition";

afterEach(() => {
	window.getSelection()?.removeAllRanges();
	document.body.replaceChildren();
	vi.restoreAllMocks();
});

function setup(clipped = false) {
	const viewport = document.createElement("div");
	const source = document.createElement("div");
	source.dataset.chatMessageId = "source";
	source.textContent = "Exact selected text";
	viewport.append(source);
	const button = document.createElement("div");
	document.body.append(viewport, button);
	const clip = document.createElement("div");
	if (clipped) {
		clip.style.overflowY = "hidden"; document.body.append(clip); clip.append(viewport);
		vi.spyOn(clip, "getBoundingClientRect").mockReturnValue(new DOMRect(0,180,500,150));
	}
	let selectionTop = 200;
	let selectionHeight = 40;
	let selectionLeft = 200;
	let viewportWidth = 500;
	const rect = (x: number, y: number, width: number, height: number) => new DOMRect(x, y, width, height);
	vi.spyOn(viewport, "getBoundingClientRect").mockImplementation(() => rect(0, 100, viewportWidth, 400));
	vi.spyOn(button, "getBoundingClientRect").mockImplementation(() => rect(0, 0, 120, 36));
	const range = document.createRange();
	range.selectNodeContents(source);
	const readRects = vi.fn(() => [rect(selectionLeft, selectionTop, 80, 20), rect(20, selectionTop + 20, 300, 20)]);
	Object.defineProperty(range, "getClientRects", { value: readRects });
	Object.defineProperty(range, "getBoundingClientRect", { value: () => rect(20, selectionTop, 300, selectionHeight) });
	window.getSelection()?.addRange(range);
	const invalid = vi.fn();
	let scheduled: FrameRequestCallback | undefined;
	vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => { scheduled = callback; return 1; });
	const hook = renderHook(() => useChatSelectionPosition(range, { current: viewport }, { current: button }, invalid));
	return {
		...hook, viewport, source, invalid, clip, readRects,
		move(top: number) { selectionTop = top; },
		tall() { selectionHeight = 2000; },
		narrow() { viewportWidth = 160; selectionLeft = 140; },
		flush() { act(() => { const callback = scheduled; scheduled = undefined; callback?.(0); }); },
	};
}

describe("selection action positioning", () => {
	it("follows a tall selection's start, pins only after it scrolls above, and follows again on return", () => {
		const h = setup(); h.tall();
		fireEvent.scroll(h.viewport); h.flush();
		expect(h.result.current).toMatchObject({ top: 58, visible: true });
		h.move(50); fireEvent.scroll(h.viewport); h.flush();
		expect(h.result.current).toMatchObject({ top: 8, visible: true });
		h.move(200); fireEvent.scroll(h.viewport); h.flush();
		expect(h.result.current).toMatchObject({ top: 58, visible: true });
		h.move(-2500); fireEvent.scroll(h.viewport); h.flush();
		expect(h.result.current?.visible).toBe(false);
	});
	it("follows the first selected line, hides offscreen, and reappears without writing scrollTop", () => {
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

	it("flips below a visible first line near the pane top, then pins when it scrolls above", () => {
		const h = setup(); h.tall();
		h.move(120); fireEvent.scroll(h.viewport); h.flush();
		expect(h.result.current).toMatchObject({ top: 46, visible: true });
		h.move(99); fireEvent.scroll(h.viewport); h.flush();
		expect(h.result.current).toMatchObject({ top: 8, visible: true });
		h.move(200); fireEvent.scroll(h.viewport); h.flush();
		expect(h.result.current?.top).toBe(58);
	});

	it("ignores unrelated scrollers and coalesces viewport/ancestor scrolls", () => {
		const h = setup(true);
		const unrelated = document.createElement("div"); document.body.append(unrelated);
		h.readRects.mockClear();
		fireEvent.scroll(unrelated); fireEvent.scroll(h.source); h.flush();
		expect(h.readRects).not.toHaveBeenCalled();
		fireEvent.scroll(h.viewport); fireEvent.scroll(h.clip); fireEvent.scroll(document); h.flush();
		expect(h.readRects).toHaveBeenCalledOnce();
	});

	it("clamps using measured button width after a resize", () => {
		const h = setup();
		h.narrow();
		fireEvent(window, new Event("resize"));
		h.flush();
		expect(h.result.current?.left).toBe(32);
	});
	it("pins within a clipping ancestor rather than the outer window", () => {
		const h = setup(true);
		expect(h.result.current).toMatchObject({ top:46,visible:true });
		h.move(350); fireEvent.scroll(h.clip); h.flush();
		expect(h.result.current?.visible).toBe(false);
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

	it("places a page selection from its frame's box and follows only that frame's reports", () => {
		const viewport = document.createElement("div");
		const frame = document.createElement("iframe");
		const other = document.createElement("iframe");
		viewport.append(frame, other);
		const button = document.createElement("div");
		document.body.append(viewport, button);
		vi.spyOn(viewport, "getBoundingClientRect").mockReturnValue(new DOMRect(0, 100, 500, 400));
		vi.spyOn(button, "getBoundingClientRect").mockReturnValue(new DOMRect(0, 0, 120, 36));
		vi.spyOn(frame, "getBoundingClientRect").mockReturnValue(new DOMRect(20, 150, 400, 300));
		let scheduled: FrameRequestCallback | undefined;
		vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => { scheduled = callback; return 1; });
		const flush = () => act(() => { const callback = scheduled; scheduled = undefined; callback?.(0); });
		const report = (source: Window | null, top: number) => act(() => {
			const at = { left: 100, top, width: 80, height: 20 };
			window.dispatchEvent(new MessageEvent("message", { source, data: { jsonrpc: "2.0", method: "ao/selection", params: { first: at, extent: at } } }));
		});
		const range = document.createRange();
		range.selectNode(frame);
		const invalid = vi.fn();
		const at = { left: 100, top: 100, width: 80, height: 20 };
		const selected = { element: frame, first: at, extent: at };
		const hook = renderHook(({ pinned }) => useChatSelectionPosition(range, { current: viewport }, { current: button }, invalid, pinned, selected), { initialProps: { pinned: false } });
		expect(hook.result.current).toEqual({ left: 100, top: 108, visible: true });
		fireEvent(document, new Event("selectionchange")); flush();
		expect(invalid).not.toHaveBeenCalled();
		report(other.contentWindow, 300); flush();
		expect(hook.result.current?.top).toBe(108);
		report(frame.contentWindow, 300); flush();
		expect(hook.result.current).toEqual({ left: 100, top: 308, visible: false });
		hook.rerender({ pinned: true });
		expect(hook.result.current?.top).toBe(308);
		frame.remove();
		fireEvent.scroll(viewport); flush();
		expect(invalid).toHaveBeenCalled();
	});
});
