import { render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QrCanvas } from "./QrCanvas";

// jsdom has no 2D context or Path2D. The backing store size is what is under
// test, so a context that accepts every call is enough.
class FakePath2D {
	rect() {}
	roundRect() {}
	arc() {}
}

function fakeContext(): CanvasRenderingContext2D {
	return new Proxy({}, { get: (_target, prop) => (prop === "canvas" ? undefined : () => undefined) }) as CanvasRenderingContext2D;
}

beforeEach(() => {
	vi.stubGlobal("Path2D", FakePath2D);
	vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation(fakeContext as never);
});

afterEach(() => {
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

describe("QrCanvas", () => {
	// A new canvas is 300×150. At 125% scaling the 240 px pairing code needs
	// 300 device px, so a check on the width alone skipped the resize and left
	// the height at 150: the code drew stretched 2× tall with its bottom half
	// cut off, which no phone can scan.
	it.each([1, 1.25, 1.5, 2])("sizes the backing store square at devicePixelRatio %s", (dpr) => {
		vi.stubGlobal("devicePixelRatio", dpr);
		const { container } = render(<QrCanvas value="aomobile://pair#code" size={240} showLogo={false} />);
		const canvas = container.querySelector("canvas") as HTMLCanvasElement;
		const pixels = Math.round(240 * dpr);
		expect({ width: canvas.width, height: canvas.height }).toEqual({ width: pixels, height: pixels });
	});
});
