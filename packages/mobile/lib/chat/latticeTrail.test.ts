import { describe, expect, it } from "vitest";

import { LATTICE_SIDE, LATTICE_TRAIL, LATTICE_COMET, LATTICE_EDGE_OPACITY, latticeCellOrigin, latticeCometPath, latticeTrailOpacity } from "./latticeTrail";

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

	it("holds each comet dot on the edge dot it has reached for the whole step, clockwise, and wraps onto the lap", () => {
		const dot = 3;
		const gap = 1;
		const steps = LATTICE_TRAIL.length;
		// What Animated's interpolate (clamped, linear between points) reads at `value`.
		const read = (path: ReturnType<typeof latticeCometPath>, value: number) => {
			const { inputRange: input } = path;
			let i = 0;
			while (i < input.length - 2 && value > input[i + 1]!) i++;
			const t = Math.min(1, Math.max(0, (value - input[i]!) / (input[i + 1]! - input[i]!)));
			return { left: path.left[i]! + (path.left[i + 1]! - path.left[i]!) * t, top: path.top[i]! + (path.top[i + 1]! - path.top[i]!) * t };
		};
		for (const behind of [0, 2]) {
			const path = latticeCometPath(behind, dot, gap);
			for (let i = 1; i < path.inputRange.length; i++) expect(path.inputRange[i]!).toBeGreaterThan(path.inputRange[i - 1]!);
			expect(path.inputRange[0]).toBe(0);
			expect(path.inputRange.at(-1)).toBe(steps);
			for (let step = 0; step < steps; step++) {
				const cell = latticeCellOrigin(LATTICE_TRAIL[(step - behind + steps) % steps]!, dot, gap);
				for (const into of [0, 0.5, 0.99]) expect(read(path, step + into)).toEqual(cell);
			}
			expect(read(path, steps)).toEqual(read(path, 0));
		}
	});
});
