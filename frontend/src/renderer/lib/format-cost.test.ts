import { describe, expect, it } from "vitest";
import { formatCostNanos, formatEstimatedCost } from "./format-cost";

describe("formatEstimatedCost", () => {
	it.each([
		["complete", 1_234_000_000, "$1.23"],
		["partial", 1_234_000_000, "$1.23"],
		["complete", 0, "$0.00"],
		["complete", 4_200_000, "$0.0042"],
		["partial", 1, "$0.000000001"],
	] as const)("formats %s coverage for %d nano-USD", (coverage, totalNanos, expected) => {
		expect(
			formatEstimatedCost({
				cachedInputNanos: null,
				coverage,
				inputNanos: null,
				outputNanos: null,
				providerAttribution: "observed",
				totalNanos,
			}),
		).toBe(expected);
	});

	it("returns no value when an estimate is unavailable", () => {
		expect(formatEstimatedCost(null)).toBeNull();
		expect(formatEstimatedCost(undefined)).toBeNull();
	});
});

describe("formatCostNanos", () => {
	it.each([
		[null, null],
		[0, "$0.00"],
		[2_500_000_000, "$2.50"],
		[7_500_000, "$0.0075"],
	] as const)("formats component nano-USD %s", (nanos, expected) => {
		expect(formatCostNanos(nanos)).toBe(expected);
	});
});
