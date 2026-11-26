import { describe, expect, it } from "vitest";
import { isFullSwipe, swipeOffset, swipeReleaseTarget, WORKER_ACTION_REVEAL_WIDTH } from "./worker-row-swipe-model";

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

// The row's own swipe follows Gesture Handler's swipeable, which it replaced
// to keep a resting row cheap: the same reach, the same toss and threshold.
describe("swipeOffset", () => {
	it("follows the finger leftward from where the drag began, never rightward past rest", () => {
		expect(swipeOffset(0, -40, true)).toBe(-40);
		expect(swipeOffset(-WORKER_ACTION_REVEAL_WIDTH, 30, true)).toBe(-42);
		expect(swipeOffset(0, 50, true)).toBe(0);
		expect(swipeOffset(-WORKER_ACTION_REVEAL_WIDTH, 120, true)).toBe(0);
	});

	it("stops at the rail unless the row can be swiped away", () => {
		expect(swipeOffset(0, -300, false)).toBe(-WORKER_ACTION_REVEAL_WIDTH);
		expect(swipeOffset(0, -300, true)).toBe(-300);
	});
});

describe("swipeReleaseTarget", () => {
	it("opens a resting row dragged past half the rail, and settles a shorter drag back", () => {
		expect(swipeReleaseTarget(false, -37, 0)).toBe(-WORKER_ACTION_REVEAL_WIDTH);
		expect(swipeReleaseTarget(false, -35, 0)).toBe(0);
	});

	it("counts a flick's speed, as a toss of a twentieth of it", () => {
		expect(swipeReleaseTarget(false, -20, -400)).toBe(-WORKER_ACTION_REVEAL_WIDTH);
		expect(swipeReleaseTarget(false, -40, 200)).toBe(0);
	});

	it("keeps an open row open unless the drag carries it back past half the rail", () => {
		expect(swipeReleaseTarget(true, 30, 0)).toBe(-WORKER_ACTION_REVEAL_WIDTH);
		expect(swipeReleaseTarget(true, 37, 0)).toBe(0);
		expect(swipeReleaseTarget(true, 10, 600)).toBe(0);
	});
});
