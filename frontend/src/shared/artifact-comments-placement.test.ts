import { describe, expect, it } from "vitest";
import { placeCommentsPopover } from "./artifact-comments";

// A 700px frame; margins and gaps are 8px.
describe("placeCommentsPopover", () => {
	it("stays at the top with no selection", () => {
		expect(placeCommentsPopover(null, 700, 300)).toEqual({ top: 8, maxHeight: 684 });
	});

	it("sits 8px below the element when it fits there", () => {
		expect(placeCommentsPopover({ top: 100, bottom: 180 }, 700, 300)).toEqual({ top: 188, maxHeight: 504 });
	});

	it("sits 8px above the element when it only fits there", () => {
		expect(placeCommentsPopover({ top: 500, bottom: 600 }, 700, 300)).toEqual({ top: 192, maxHeight: 484 });
	});

	it("takes the roomier side, capped, when it fits neither", () => {
		// 342px below, 184px above: below, capped to 342.
		expect(placeCommentsPopover({ top: 200, bottom: 342 }, 700, 500)).toEqual({ top: 350, maxHeight: 342 });
		// 92px below, 384px above: above, capped to 384 and ending 8px over the element.
		expect(placeCommentsPopover({ top: 400, bottom: 592 }, 700, 500)).toEqual({ top: 8, maxHeight: 384 });
	});

	it("stays inside the frame beside an element taller than the frame or scrolled away", () => {
		const tall = placeCommentsPopover({ top: -300, bottom: 1200 }, 700, 400);
		expect(tall.top).toBeGreaterThanOrEqual(8);
		expect(tall.top + tall.maxHeight).toBeLessThanOrEqual(692);
		expect(tall.maxHeight).toBe(120);
		expect(placeCommentsPopover({ top: -500, bottom: -400 }, 700, 300)).toEqual({ top: 8, maxHeight: 684 });
		expect(placeCommentsPopover({ top: 900, bottom: 1000 }, 700, 300)).toEqual({ top: 8, maxHeight: 684 });
	});
});
