import { describe, expect, it } from "vitest";
import { humanPath } from "./human-path";

describe("humanPath", () => {
	it("ends exactly on the target with a bounded number of steps", () => {
		const path = humanPath({ x: 0, y: 0 }, { x: 400, y: 300 }, () => 0.9);
		expect(path.at(-1)).toEqual({ x: 400, y: 300 });
		expect(path.length).toBeGreaterThanOrEqual(8);
		expect(path.length).toBeLessThanOrEqual(40);
	});

	it("bows away from the straight line", () => {
		const path = humanPath({ x: 0, y: 0 }, { x: 400, y: 0 }, () => 1);
		expect(Math.max(...path.map((point) => Math.abs(point.y)))).toBeGreaterThan(10);
	});

	it("handles zero distance", () => {
		expect(humanPath({ x: 5, y: 5 }, { x: 5, y: 5 }).at(-1)).toEqual({ x: 5, y: 5 });
	});
});
