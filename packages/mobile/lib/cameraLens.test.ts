import { describe, expect, it } from "vitest";
import { NORMAL_LENS, pickNormalLens } from "./cameraLens";

// The list a modern multi-lens iPhone reports, sorted as the native side sorts it.
const TRIPLE = ["Back Camera", "Back Dual Wide Camera", "Back Telephoto Camera", "Back Triple Camera", "Back Ultra Wide Camera"];

describe("pickNormalLens", () => {
	it("picks the plain 1x lens over the ultra-wide and the virtual devices", () => {
		expect(pickNormalLens(TRIPLE)).toBe(NORMAL_LENS);
	});

	it("picks it on a dual-camera phone too", () => {
		expect(pickNormalLens(["Back Camera", "Back Dual Wide Camera", "Back Ultra Wide Camera"])).toBe(NORMAL_LENS);
	});

	it("picks it on a single-camera phone", () => {
		expect(pickNormalLens(["Back Camera"])).toBe(NORMAL_LENS);
	});

	// A selectedLens matching no device leaves the session with no camera, so an
	// empty or unrecognisable list must defer to the native default.
	it("returns undefined for an empty list", () => {
		expect(pickNormalLens([])).toBeUndefined();
	});

	it("returns undefined when every lens is a specialised optic", () => {
		expect(pickNormalLens(["Back Ultra Wide Camera", "Back Triple Camera"])).toBeUndefined();
	});

	describe("non-English devices, where the exact name won't match", () => {
		it("prefers the unqualified lens by name length", () => {
			// de-DE style naming: the plain lens carries no qualifier.
			const german = ["Rückkamera", "Ultraweitwinkel-Rückkamera", "Tele-Rückkamera"];
			expect(pickNormalLens(german)).toBe("Rückkamera");
		});

		it("still drops virtual devices in other locales", () => {
			const fr = ["Appareil arrière", "Appareil arrière triple", "Appareil arrière ultra grand-angle"];
			expect(pickNormalLens(fr)).toBe("Appareil arrière");
		});
	});
});
