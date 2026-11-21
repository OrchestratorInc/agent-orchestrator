import { describe, expect, it } from "vitest";
import { atTail, followsAfterRest, openingRow, RESUME_SLOP, tailFollowStep, TAIL_SLOP } from "./timelineScroll";

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

describe("openingRow", () => {
	it("opens on the newest rows, or the first when there are fewer", () => {
		expect(openingRow(30, 4)).toBe(26);
		expect(openingRow(4, 4)).toBe(0);
		expect(openingRow(2, 4)).toBe(0);
	});
});


describe("tailFollowStep", () => {
	it("moves toward a growing end across frames and settles at the measured end", () => {
		const first = tailFollowStep(100, 120, 600, 16, 20, 0);
		expect(first).toBeGreaterThan(100);
		expect(first).toBeLessThan(120);
		const second = tailFollowStep(first, 140, 600, 16, 20, 0);
		expect(second).toBeGreaterThan(first);
		expect(second).toBeLessThan(140);
		let position = second;
		for (let i = 1; i <= 60; i++) position = tailFollowStep(position, 140, 600, 16, 20, i * 16);
		expect(position).toBe(140);
	});

	it("uses recent growth as a lead without scrolling past measured content", () => {
		const recent = tailFollowStep(100, 120, 600, 16, 20, 0);
		const old = tailFollowStep(100, 120, 600, 16, 20, 250);
		expect(recent).toBeGreaterThan(old);
		expect(tailFollowStep(119, 120, 600, 16, 80, 0)).toBe(120);
	});

	it("snaps on shrink, a long interruption, or an end beyond one screen", () => {
		expect(tailFollowStep(200, 120, 600, 16, 0, 0)).toBe(120);
		expect(tailFollowStep(100, 150, 600, 1200, 20, 0)).toBe(150);
		expect(tailFollowStep(100, 800, 600, 16, 700, 0)).toBe(800);
	});
});
