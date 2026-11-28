import { describe, expect, it } from "vitest";
import { atTail, followsAfterRest, openingRow, RESUME_SLOP, TAIL_SLOP, visibleContentAnchor } from "./timelineScroll";

const metrics = (offset: number) => ({ contentOffset: { x: 0, y: offset }, contentSize: { width: 390, height: 3000 }, layoutMeasurement: { width: 390, height: 800 } });

describe("atTail", () => {
	it("counts the end, and a thumb's slop short of it, as the newest message", () => {
		expect(atTail(metrics(2200))).toBe(true);
		expect(atTail(metrics(2200 - TAIL_SLOP + 1))).toBe(true);
		expect(atTail(metrics(2200 - TAIL_SLOP))).toBe(false);
		expect(atTail(metrics(0))).toBe(false);
	});

	// The keyboard's room below the content moves the end further down.
	it("measures from the end the list has made room to", () => {
		expect(atTail(metrics(2200), 300)).toBe(false);
		expect(atTail(metrics(2500), 300)).toBe(true);
	});

	// A short flick up to start reading lets go inside the thumb's slop; it must
	// not count as resting at the newest message, or new output pulls it back.
	it("takes a tighter slop for a scroll to rest at the newest message", () => {
		expect(RESUME_SLOP).toBeLessThan(TAIL_SLOP);
		expect(atTail(metrics(2200 - RESUME_SLOP + 1), 0, RESUME_SLOP)).toBe(true);
		expect(atTail(metrics(2200 - RESUME_SLOP), 0, RESUME_SLOP)).toBe(false);
		expect(atTail(metrics(2200 - 84), 0, RESUME_SLOP)).toBe(false);
	});
});

describe("followsAfterRest", () => {
	// Android's still momentum after letting go at the end: output moved the end,
	// not the reader, so it keeps following.
	it("keeps the release's verdict when the list has not moved since", () => {
		expect(followsAfterRest({ offset: 2200, atEnd: true }, 2200, false)).toBe(true);
		expect(followsAfterRest({ offset: 1600, atEnd: false }, 1600, true)).toBe(false);
	});
	// Output that grew and folded away again in that window changes nothing either.
	it("ignores output that grew and folded away before the momentum ended", () => {
		expect(followsAfterRest({ offset: 2200, atEnd: true }, 2200, true)).toBe(true);
	});
	// A fling up from the end, and history added above (scroll anchoring moves
	// the offset with it), are measured where the list rests.
	it("measures where the list rests once it has moved since the release", () => {
		expect(followsAfterRest({ offset: 2200, atEnd: true }, 1400, false)).toBe(false);
		expect(followsAfterRest({ offset: 0, atEnd: false }, 1000, false)).toBe(false);
		expect(followsAfterRest(undefined, 2200, true)).toBe(true);
		expect(followsAfterRest(undefined, 2200, false)).toBe(false);
	});
});

describe("visibleContentAnchor", () => {
	// iOS anchored to the list's spacer for rows not drawn yet when a follow
	// landed in it. Drawing those rows recycled the spacer and moved the offset
	// back by the spacer's height: a long chat opened part way up its transcript.
	it("holds nothing in place on iOS while the list follows the newest message", () => {
		expect(visibleContentAnchor("ios", true)).toBeUndefined();
	});
	it("holds what the person reads in place when they have scrolled up", () => {
		expect(visibleContentAnchor("ios", false)).toEqual({ minIndexForVisible: 0 });
	});
	it("keeps Android's anchor either way", () => {
		expect(visibleContentAnchor("android", true)).toEqual({ minIndexForVisible: 0 });
		expect(visibleContentAnchor("android", false)).toEqual({ minIndexForVisible: 0 });
	});
	it("returns the same anchor each time, so the list's props stay put", () => {
		expect(visibleContentAnchor("ios", false)).toBe(visibleContentAnchor("android", true));
	});
});

describe("openingRow", () => {
	it("opens on the newest rows, or the first when there are fewer", () => {
		expect(openingRow(30, 4)).toBe(26);
		expect(openingRow(4, 4)).toBe(0);
		expect(openingRow(2, 4)).toBe(0);
	});
});
