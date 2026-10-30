import { describe, expect, it } from "vitest";
import { estimatedLoadProgress, loadProgressPercent } from "./loadProgress";

describe("load progress", () => {
	it("starts an unreported load visibly and slows as it nears the end without reaching it", () => {
		expect(estimatedLoadProgress(0)).toBeCloseTo(0.1);
		const early = estimatedLoadProgress(500) - estimatedLoadProgress(0);
		const late = estimatedLoadProgress(5_500) - estimatedLoadProgress(5_000);
		expect(early).toBeGreaterThan(late);
		expect(estimatedLoadProgress(60_000)).toBeLessThan(0.91);
		expect(estimatedLoadProgress(-10)).toBeCloseTo(0.1);
	});

	it("reads progress as a whole percentage", () => {
		expect(loadProgressPercent(0.426)).toBe(43);
		expect(loadProgressPercent(1.2)).toBe(100);
		expect(loadProgressPercent(-1)).toBe(0);
	});
});
