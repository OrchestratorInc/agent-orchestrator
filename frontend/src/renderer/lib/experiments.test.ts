import { describe, expect, it } from "vitest";
import { firstRunPromptVariant } from "./experiments";

describe("firstRunPromptVariant", () => {
	it.each([
		[0, "prompt"], [49, "prompt"], [50, "control"], [99, "control"], [null, "control"],
		[-1, "control"], [100, "control"], [0.5, "control"], [NaN, "control"],
	] as const)("assigns cohort %s to %s", (cohort, variant) => {
		expect(firstRunPromptVariant(cohort)).toBe(variant);
	});
});
