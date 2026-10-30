import { describe, expect, it } from "vitest";
import { effortGaugeSymbol, spawnEffortChoices } from "./modelEffort";

const levels = (selected?: string) => ["low", "medium", "high"].map((value) => ({ value, label: value, selected: value === selected }));

describe("effort speedometer", () => {
	it("points the needle at the chosen level's place among the model's", () => {
		expect(effortGaugeSymbol(levels("low"))).toBe("gauge.low");
		expect(effortGaugeSymbol(levels("medium"))).toBe("gauge.medium");
		expect(effortGaugeSymbol(levels("high"))).toBe("gauge.high");
		const five = ["minimal", "low", "medium", "high", "max"].map((value) => ({ value, label: value, selected: value === "max" }));
		expect(effortGaugeSymbol(five)).toBe("gauge.high");
	});

	it("points to the middle with no level chosen", () => {
		expect(effortGaugeSymbol(levels())).toBe("gauge.medium");
	});
});

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

	it("offers nothing for a model that takes no effort", () => {
		expect(spawnEffortChoices({ efforts: [] }, undefined)).toEqual([]);
		expect(spawnEffortChoices(undefined, "high")).toEqual([]);
	});
});
