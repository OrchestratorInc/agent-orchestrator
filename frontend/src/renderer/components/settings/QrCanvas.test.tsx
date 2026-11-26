import { render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { paintQr, quietZonePx } from "./qr-paint";
import { PAIRING_QR_VERSION, QrCanvas } from "./QrCanvas";

// The quiet-zone tests read the matrix and margin where QrCanvas hands them to
// the painter; qr-paint.test.ts covers what the painter does with them.
vi.mock("./qr-paint", async (importOriginal) => ({
	...(await importOriginal<typeof import("./qr-paint")>()),
	paintQr: vi.fn(),
}));

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
	vi.mocked(paintQr).mockClear();
	vi.stubGlobal("Path2D", FakePath2D);
	vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation(fakeContext as never);
});

const SIZE = 240;

function pairingPayload(bytes: number): string {
	const prefix = "aomobile://pair#";
	return prefix + "a".repeat(bytes - prefix.length);
}

function painted(value: string, version?: number) {
	render(<QrCanvas value={value} size={SIZE} version={version} showLogo={false} />);
	expect(paintQr).toHaveBeenCalledTimes(1);
	const [, matrix, { margin }] = vi.mocked(paintQr).mock.calls[0];
	const cell = (SIZE - 2 * margin) / matrix.modules;
	return { modules: matrix.modules, margin, quietZone: margin / cell };
}

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

describe("QrCanvas quiet zone", () => {
	it("is four modules of the pinned version when the payload fits it", () => {
		const { modules, margin, quietZone } = painted(pairingPayload(400), PAIRING_QR_VERSION);
		expect(modules).toBe(85);
		expect(margin).toBe(quietZonePx(SIZE, 85));
		expect(quietZone).toBeGreaterThanOrEqual(4);
		expect(quietZone).toBeLessThan(4.5);
	});

	// The pin only sets the density: a payload too long for it is drawn at a
	// bigger version, and the quiet zone has to follow the modules painted.
	it("is four modules of the matrix painted when the payload overflows the pin", () => {
		const { modules, margin, quietZone } = painted(pairingPayload(505), PAIRING_QR_VERSION);
		expect(modules).toBe(89);
		expect(margin).toBe(quietZonePx(SIZE, 89));
		expect(quietZone).toBeGreaterThanOrEqual(4);
		expect(quietZone).toBeLessThan(4.1);
	});

	it("follows the encoder's choice when no version is pinned", () => {
		const { modules, margin } = painted(pairingPayload(40));
		expect(modules).toBeLessThan(85);
		expect(margin).toBe(quietZonePx(SIZE, modules));
	});
});
