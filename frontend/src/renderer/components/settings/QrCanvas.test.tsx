import { render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { paintQr, quietZonePx } from "./qr-paint";
import { PAIRING_QR_VERSION, QrCanvas } from "./QrCanvas";

// jsdom has no canvas, so read the matrix and margin where QrCanvas hands them
// to the painter. qr-paint.test.ts covers what the painter does with them.
vi.mock("./qr-paint", async (importOriginal) => ({
	...(await importOriginal<typeof import("./qr-paint")>()),
	paintQr: vi.fn(),
}));

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

beforeEach(() => {
	vi.mocked(paintQr).mockClear();
	vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue({
		setTransform: () => undefined,
	} as unknown as ReturnType<HTMLCanvasElement["getContext"]>);
});

afterEach(() => {
	vi.restoreAllMocks();
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
