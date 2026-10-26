import { describe, expect, it } from "vitest";
import { isFullSwipe, WORKER_ACTION_REVEAL_WIDTH } from "./worker-row-swipe-model";

describe("isFullSwipe", () => {
	it("never archives a drag that only opens the rail", () => {
		expect(isFullSwipe(-WORKER_ACTION_REVEAL_WIDTH, 390)).toBe(false);
		expect(isFullSwipe(-200, 390)).toBe(false);
	});

	it("archives once the drag uncovers most of the row", () => {
		expect(isFullSwipe(-234, 390)).toBe(true);
		expect(isFullSwipe(-380, 390)).toBe(true);
	});

	// On a narrow row 60% would sit inside the rail itself.
	it("keeps clear of the rail on a narrow row", () => {
		expect(isFullSwipe(-150, 240)).toBe(false);
		expect(isFullSwipe(-192, 240)).toBe(true);
	});

	it("ignores rightward drags and a row that has not been measured", () => {
		expect(isFullSwipe(300, 390)).toBe(false);
		expect(isFullSwipe(-300, 0)).toBe(false);
	});
});
