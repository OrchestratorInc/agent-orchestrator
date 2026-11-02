import { describe, expect, it } from "vitest";
import { base64ToBytes, bytesToBase64, pngSize } from "./screenshot";

// 1x1 transparent PNG.
const PNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==";

describe("screenshot helpers", () => {
	it("reads PNG dimensions and round-trips base64", () => {
		const bytes = base64ToBytes(PNG);
		expect(pngSize(bytes)).toEqual({ width: 1, height: 1 });
		expect(bytesToBase64(bytes)).toBe(PNG);
	});

	it("rejects non-PNG data", () => {
		expect(() => pngSize(new Uint8Array(30))).toThrow(expect.objectContaining({ code: "SCREENSHOT_UNAVAILABLE" }));
	});
});
