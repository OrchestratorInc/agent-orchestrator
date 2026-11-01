import { describe, expect, it } from "vitest";
import { effortGaugeNeedle, effortGaugeSegments, fallbackEffortIndex, magneticOffset, nearestStop, spawnEffortChoices, withFallbackEffort } from "./modelEffort";

const levels = (selected?: string) => ["low", "medium", "high"].map((value) => ({ value, label: value, selected: value === selected }));

describe("new task effort choices", () => {
	const opus = { efforts: ["low", "medium", "high"], defaultEffort: "medium" };

	it("checks the model's default until a level is picked", () => {
		expect(spawnEffortChoices(opus, undefined)).toEqual([
			{ value: "low", label: "Low", selected: false },
			{ value: "medium", label: "Medium", selected: true },
			{ value: "high", label: "High", selected: false },
		]);
		expect(spawnEffortChoices(opus, "high").find((choice) => choice.selected)?.value).toBe("high");
	});

	// A level picked for another model is not this one's.
	it("falls back to the default for a level this model does not take", () => {
		expect(spawnEffortChoices(opus, "max").find((choice) => choice.selected)?.value).toBe("medium");
	});

	it("checks the fallback level for a model that names no default", () => {
		expect(spawnEffortChoices({ efforts: ["low", "medium", "high", "xhigh", "max"] }, undefined).find((choice) => choice.selected)?.value).toBe("medium");
		expect(spawnEffortChoices({ efforts: ["low", "high", "max"] }, "max").find((choice) => choice.selected)?.value).toBe("max");
	});

	it("offers nothing for a model that takes no effort", () => {
		expect(spawnEffortChoices({ efforts: [] }, undefined)).toEqual([]);
		expect(spawnEffortChoices(undefined, "high")).toEqual([]);
	});
});

describe("custom effort gauge", () => {
	const levels = (selected?: string) => ["low", "medium", "high", "xhigh", "max"].map((value) => ({ value, label: value, selected: value === selected }));

	it("splits the half circle into a segment per level with even gaps", () => {
		const { count, span, gap } = effortGaugeSegments(5);
		expect(count).toBe(5);
		expect(span * 5 + gap * 4).toBeCloseTo(180);
		expect(effortGaugeSegments(0)).toEqual({ count: 1, span: 180, gap: 0 });
	});

	it("points the needle at the chosen level, straight up with none", () => {
		expect(effortGaugeNeedle(levels())).toBe(90);
		expect(effortGaugeNeedle(levels("high"))).toBeCloseTo(90);
		expect(effortGaugeNeedle(levels("low"))).toBeGreaterThan(150);
		expect(effortGaugeNeedle(levels("max"))).toBeLessThan(30);
	});
});

describe("effort slider", () => {
	it("finds the stop nearest a point, clamped to the ends", () => {
		// Stops at 20, 70, 120, 170, 220.
		expect(nearestStop(20, 20, 50, 5)).toBe(0);
		expect(nearestStop(100, 20, 50, 5)).toBe(2);
		expect(nearestStop(-50, 20, 50, 5)).toBe(0);
		expect(nearestStop(400, 20, 50, 5)).toBe(4);
		expect(nearestStop(100, 20, 0, 5)).toBe(0);
	});

	it("pulls the blob toward its stop, hardest close in, meeting the finger halfway", () => {
		expect(magneticOffset(0, 50)).toBe(0);
		expect(magneticOffset(5, 50)).toBeCloseTo(1);
		expect(magneticOffset(-12.5, 50)).toBeCloseTo(-6.25);
		expect(magneticOffset(25, 50)).toBe(25);
		expect(magneticOffset(-25, 50)).toBe(-25);
		expect(magneticOffset(10, 0)).toBe(0);
	});
});

describe("fallback effort", () => {
	it("takes Medium where the model has it", () => {
		expect(fallbackEffortIndex(["low", "medium", "high", "xhigh", "max"])).toBe(1);
		expect(fallbackEffortIndex(["minimal", "low", "Medium"])).toBe(2);
	});

	it("else the middle level, the first of two middles", () => {
		expect(fallbackEffortIndex(["low", "high", "max"])).toBe(1);
		expect(fallbackEffortIndex(["off", "low", "high", "max"])).toBe(1);
		expect(fallbackEffortIndex(["low", "high"])).toBe(0);
		expect(fallbackEffortIndex(["max"])).toBe(0);
	});

	it("checks it only when no level is checked", () => {
		const none = ["low", "medium", "high"].map((value) => ({ value, label: value, selected: false }));
		expect(withFallbackEffort(none).map((choice) => choice.selected)).toEqual([false, true, false]);
		const picked = none.map((choice) => ({ ...choice, selected: choice.value === "high" }));
		expect(withFallbackEffort(picked)).toEqual(picked);
		expect(withFallbackEffort([])).toEqual([]);
	});
});
