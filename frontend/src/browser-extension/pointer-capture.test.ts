import { afterEach, describe, expect, it, vi } from "vitest";
import { mouseCapture } from "./pointer-capture";

afterEach(() => {
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
	delete (globalThis as unknown as { aoMouseCapture?: unknown }).aoMouseCapture;
	document.body.innerHTML = "";
});

function prepare() {
	const listen = vi.spyOn(window, "addEventListener").mockImplementation(() => undefined);
	expect(mouseCapture("track")).toBe(true);
	return listen.mock.calls[0]![1] as (event: Partial<PointerEvent>) => void;
}

describe("isolated mouse capture cleanup", () => {
	it("uses the observed mouse ID and traverses a closed shadow root", () => {
		const pointerDown = prepare();
		const host = document.createElement("div");
		document.body.append(host);
		const root = host.attachShadow({ mode: "closed" });
		const button = document.createElement("button");
		root.append(button);
		const release = vi.fn();
		vi.stubGlobal("chrome", { dom: { openOrClosedShadowRoot: (element: Element) => element === host ? root : null } });
		vi.spyOn(Element.prototype, "hasPointerCapture").mockImplementation(function (this: Element, id) {
			return this === button && id === 47;
		});
		vi.spyOn(Element.prototype, "releasePointerCapture").mockImplementation(release);
		pointerDown({ isTrusted: true, pointerType: "mouse", pointerId: 47, buttons: 1 });
		expect(mouseCapture("release")).toBe(true);
		expect(release).toHaveBeenCalledWith(47);
	});

	it("tracks a held mouse entering a frame and ignores unpressed movement", () => {
		const listen = vi.spyOn(window, "addEventListener").mockImplementation(() => undefined);
		mouseCapture("track");
		const observe = listen.mock.calls.find(([name]) => name === "pointermove")![1] as (event: Partial<PointerEvent>) => void;
		observe({ isTrusted: true, pointerType: "mouse", pointerId: 47, buttons: 0 });
		expect(mouseCapture("release")).toBe("idle");
		observe({ isTrusted: true, pointerType: "mouse", pointerId: 47, buttons: 1 });
		expect(mouseCapture("release")).toBe(true);
	});

	it("refuses an unprepared document", () => {
		expect(mouseCapture("release")).toBe(false);
	});

	it("releases another frame's observed pointer when the local observer was suppressed", () => {
		prepare();
		expect(mouseCapture("observed")).toBe("idle");
		const button = document.createElement("button");
		document.body.append(button);
		vi.spyOn(Element.prototype, "hasPointerCapture").mockImplementation(function (this: Element, id) {
			return this === button && id === 47;
		});
		const release = vi.spyOn(Element.prototype, "releasePointerCapture").mockImplementation(() => undefined);
		expect(mouseCapture("release", [47])).toBe(true);
		expect(release).toHaveBeenCalledExactlyOnceWith(47);
	});

	it("does not claim cleanup when no trusted mouse press was observed", () => {
		const pointerDown = prepare();
		pointerDown({ isTrusted: false, pointerType: "mouse", pointerId: 2 });
		pointerDown({ isTrusted: true, pointerType: "pen", pointerId: 3 });
		expect(mouseCapture("release")).toBe("idle");
	});
});
