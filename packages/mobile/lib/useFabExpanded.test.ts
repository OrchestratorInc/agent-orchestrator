import { describe, expect, it, vi } from "vitest";

vi.mock("react-native", () => ({ Platform: { OS: "android" } }));

import { nextFabExpanded } from "./useFabExpanded";

describe("nextFabExpanded", () => {
	it("folds to the icon once the list moves down", () => {
		expect(nextFabExpanded(true, 100, 140)).toBe(false);
	});

	it("opens again on any real scroll up", () => {
		expect(nextFabExpanded(false, 400, 360)).toBe(true);
	});

	it("stays as it is for a move too small to call, so a resting finger does not flicker it", () => {
		expect(nextFabExpanded(false, 400, 405)).toBe(false);
		expect(nextFabExpanded(true, 400, 395)).toBe(true);
	});

	it("is always open at the top of the list, overscroll included", () => {
		expect(nextFabExpanded(false, 300, 0)).toBe(true);
		expect(nextFabExpanded(false, 0, -40)).toBe(true);
	});
});
