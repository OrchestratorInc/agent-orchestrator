import { describe, expect, it } from "vitest";
import { fieldRevealOffset } from "./fieldReveal";

// The region the list shows: below the header at 100, above the composer on the keyboard at 500.
const region = { visibleTop: 100, visibleBottom: 500, offset: 1_000 };

describe("revealing a focused request field", () => {
	// Focusing a field already in view must not nudge the conversation (the
	// jitter `scrollsChildToFocus={false}` removed).
	it("leaves a field in view where it is", () => {
		expect(fieldRevealOffset({ ...region, fieldTop: 200, fieldBottom: 260 })).toBeUndefined();
		expect(fieldRevealOffset({ ...region, fieldTop: 100, fieldBottom: 500 })).toBeUndefined();
	});

	it("lifts a field under the keyboard just clear of it", () => {
		expect(fieldRevealOffset({ ...region, fieldTop: 560, fieldBottom: 620 })).toBe(1_120);
		// Partly hidden: only the hidden part.
		expect(fieldRevealOffset({ ...region, fieldTop: 470, fieldBottom: 530 })).toBe(1_030);
	});

	it("brings down a field above the region, under the header", () => {
		expect(fieldRevealOffset({ ...region, fieldTop: 40, fieldBottom: 100 })).toBe(940);
	});

	// A field taller than the region shows from its top, where its label is.
	it("shows a field taller than the region from its top", () => {
		expect(fieldRevealOffset({ ...region, fieldTop: 300, fieldBottom: 900 })).toBe(1_200);
		expect(fieldRevealOffset({ ...region, fieldTop: 50, fieldBottom: 650 })).toBe(950);
	});
});
