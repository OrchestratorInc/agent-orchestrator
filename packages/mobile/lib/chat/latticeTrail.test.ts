import { afterEach, describe, expect, it, vi } from "vitest";

import { LATTICE_SIDE, LATTICE_TRAIL, LATTICE_COMET, LATTICE_EDGE_OPACITY, LATTICE_LAP_MS, createLatticeClock, latticeCellOrigin, latticeCometPath, latticeTrailOpacity } from "./latticeTrail";

// The lattice mirrors the desktop's working mark, so what is worth pinning is the
// shape of the trail and the beat each dot plays, not the animation plumbing.
describe("lattice trail", () => {
	it("runs clockwise round the outer twelve dots and leaves the inner four at rest", () => {
		const steps = Array.from({ length: LATTICE_SIDE * LATTICE_SIDE }, (_, cell) => {
			const step = LATTICE_TRAIL.indexOf(cell as (typeof LATTICE_TRAIL)[number]);
			return step < 0 ? undefined : step;
		});
		expect(steps).toEqual([
			0, 1, 2, 3,
			11, undefined, undefined, 4,
			10, undefined, undefined, 5,
			9, 8, 7, 6,
		]);
	});

	it("places every dot outright in a 4×4 grid, so no density can wrap a row", () => {
		// 420 dpi Android (PixelRatio 2.625), where a flex-wrapped row overflowed in float32.
		const dot = Math.round(2.5 * 2.625) / 2.625;
		const gap = Math.round(1 * 2.625) / 2.625;
		const origins = Array.from({ length: LATTICE_SIDE * LATTICE_SIDE }, (_, cell) => latticeCellOrigin(cell, dot, gap));
		const columns = new Set(origins.map((o) => o.left));
		const rows = new Set(origins.map((o) => o.top));
		expect(columns.size).toBe(4);
		expect(rows.size).toBe(4);
		expect(latticeCellOrigin(3, dot, gap)).toEqual({ left: 3 * (dot + gap), top: 0 });
		expect(latticeCellOrigin(12, dot, gap)).toEqual({ left: 0, top: 3 * (dot + gap) });
		// The last dot ends exactly at the lattice's edge.
		const size = dot * LATTICE_SIDE + gap * (LATTICE_SIDE - 1);
		expect(latticeCellOrigin(15, dot, gap).left + dot).toBeCloseTo(size, 10);
	});

	it("plays the desktop's keyframes: dim, a quick brighten, a hold, then a slower fade", () => {
		expect(latticeTrailOpacity(0)).toBeCloseTo(0.2);
		expect(latticeTrailOpacity(0.12)).toBeCloseTo(1);
		expect(latticeTrailOpacity(0.17)).toBeCloseTo(1);
		expect(latticeTrailOpacity(0.22)).toBeCloseTo(1);
		expect(latticeTrailOpacity(0.4)).toBeCloseTo(0.2);
		expect(latticeTrailOpacity(0.7)).toBeCloseTo(0.2);
		// The eased midpoint of a segment is its midpoint value.
		expect(latticeTrailOpacity(0.06)).toBeCloseTo(0.6, 2);
		expect(latticeTrailOpacity(0.31)).toBeCloseTo(0.6, 2);
		// Eased, not linear: a quarter of the way into the brighten is still dim.
		expect(latticeTrailOpacity(0.03)).toBeLessThan(0.4);
	});

	it("wraps any phase onto one lap, so a dot's offset can be subtracted from the lap", () => {
		expect(latticeTrailOpacity(-0.88)).toBeCloseTo(latticeTrailOpacity(0.12));
		expect(latticeTrailOpacity(2.31)).toBeCloseTo(latticeTrailOpacity(0.31));
	});

	it("keeps three or four dots lit at any moment, so it reads as one travelling trail", () => {
		for (let lap = 0; lap < 1; lap += 0.01) {
			const lit = LATTICE_TRAIL.filter((_, step) => latticeTrailOpacity(lap - step / LATTICE_TRAIL.length) > 0.5);
			expect(lit.length).toBeGreaterThanOrEqual(3);
			expect(lit.length).toBeLessThanOrEqual(4);
		}
	});
});

describe("the trail's comet", () => {
	it("lights the dots the desktop's trail holds lit at each step, and leaves the rest at its floor", () => {
		const steps = LATTICE_TRAIL.length;
		for (const [behind, level] of LATTICE_COMET.entries()) expect(level).toBeCloseTo(latticeTrailOpacity((behind + 1.5) / steps), 1);
		expect(latticeTrailOpacity(0.5 / steps)).toBeLessThan(0.4);
		expect(latticeTrailOpacity(4.5 / steps)).toBeCloseTo(LATTICE_EDGE_OPACITY, 1);
	});

	it("places each comet dot on the edge dot it has reached at every step, clockwise", () => {
		const dot = 3;
		const gap = 1;
		const head = latticeCometPath(0, dot, gap);
		const tail = latticeCometPath(2, dot, gap);
		expect(head.inputRange).toEqual(LATTICE_TRAIL.map((_, step) => step));
		const at = (path: typeof head, step: number) => ({ left: path.left[step], top: path.top[step] });
		for (let step = 0; step < LATTICE_TRAIL.length; step++) {
			expect(at(head, step)).toEqual(latticeCellOrigin(LATTICE_TRAIL[step]!, dot, gap));
			expect(at(tail, step)).toEqual(latticeCellOrigin(LATTICE_TRAIL[(step + LATTICE_TRAIL.length - 2) % LATTICE_TRAIL.length]!, dot, gap));
		}
	});
});

describe("the lattice clock", () => {
	afterEach(() => vi.useRealTimers());

	it("steps round the edge a twelfth of a lap at a time, only while held", () => {
		vi.useFakeTimers();
		const steps: number[] = [];
		const hold = createLatticeClock((step) => steps.push(step));
		vi.advanceTimersByTime(LATTICE_LAP_MS);
		expect(steps).toEqual([]);
		const first = hold();
		const second = hold();
		vi.advanceTimersByTime(LATTICE_LAP_MS + LATTICE_LAP_MS / 12);
		// One clock however many lattices hold it, wrapping onto the lap.
		expect(steps).toEqual([1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 0, 1]);
		first();
		first();
		vi.advanceTimersByTime(LATTICE_LAP_MS / 12);
		expect(steps).toHaveLength(14);
		second();
		vi.advanceTimersByTime(LATTICE_LAP_MS);
		expect(steps).toHaveLength(14);
	});
});
