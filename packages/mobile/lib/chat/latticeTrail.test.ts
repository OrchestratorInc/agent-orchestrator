import { describe, expect, it } from "vitest";

import { LATTICE_SIDE, LATTICE_TRAIL, latticeTrailOpacity } from "./latticeTrail";

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
