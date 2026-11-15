import { describe, expect, it } from "vitest";
import { isFullSwipe, WORKER_ACTION_REVEAL_WIDTH } from "./worker-row-swipe-model";

describe("isFullSwipe", () => {
	it("never archives a drag that only opens the rail", () => {
		expect(isFullSwipe(-WORKER_ACTION_REVEAL_WIDTH, 390)).toBe(false);
		expect(isFullSwipe(-100, 390)).toBe(false);
	});

	// The pair armed at 60% of the row with a 128pt rail; scaled to the 72pt
	// pin that is 33.75%, so on a 390pt row it arms about 60pt past the pin.
	it("arms a short drag after the pin is showing", () => {
		expect(isFullSwipe(-131, 390)).toBe(false);
		expect(isFullSwipe(-132, 390)).toBe(true);
		expect(isFullSwipe(-380, 390)).toBe(true);
	});

	// On a narrow row the share would sit inside the rail itself: at least one
	// and a half of the rail, as the pair had.
	it("keeps clear of the rail on a narrow row", () => {
		expect(isFullSwipe(-100, 240)).toBe(false);
		expect(isFullSwipe(-108, 240)).toBe(true);
	});

	it("ignores rightward drags and a row that has not been measured", () => {
		expect(isFullSwipe(300, 390)).toBe(false);
		expect(isFullSwipe(-300, 0)).toBe(false);
	});
});
