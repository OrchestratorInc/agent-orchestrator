import { describe, expect, it } from "vitest";
import { fallbackEffort } from "./effort";

describe("fallbackEffort", () => {
	it("picks the middle known level regardless of reported order", () => {
		expect(fallbackEffort(["high", "low", "xhigh", "medium"])).toBe("medium");
		expect(fallbackEffort(["low", "medium", "high"])).toBe("medium");
		expect(fallbackEffort(["low", "high"])).toBe("low");
	});

	it("keeps the provider default when it is one of the levels", () => {
		expect(fallbackEffort(["low", "medium", "high"], "high")).toBeUndefined();
	});

	it("falls back when the reported default is not a level, ignoring reset values", () => {
		expect(fallbackEffort(["default", "low", "medium", "high"], "turbo")).toBe("medium");
	});

	it("uses the reported order for levels it does not recognise", () => {
		expect(fallbackEffort(["a", "b", "c"])).toBe("b");
	});

	it("returns nothing when there are no concrete levels", () => {
		expect(fallbackEffort([])).toBeUndefined();
		expect(fallbackEffort(["default"])).toBeUndefined();
	});
});
