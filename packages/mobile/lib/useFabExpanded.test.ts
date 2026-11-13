import { describe, expect, it, vi } from "vitest";

vi.mock("react-native", () => ({ Platform: { OS: "android" } }));

import { nextFabExpanded } from "./useFabExpanded";

describe("nextFabExpanded", () => {
	// A move too small to call leaves it as it is, so a resting finger does not
	// flicker it; the top of the list, overscroll included, is always open.
	it.each([
		["folds once the list moves down", true, 100, 140, false],
		["opens on any real scroll up", false, 400, 360, true],
		["ignores a tiny move down", false, 400, 405, false],
		["ignores a tiny move up", true, 400, 395, true],
		["opens at the top", false, 300, 0, true],
		["opens in overscroll", false, 0, -40, true],
	])("%s", (_name, expanded, from, to, next) => {
		expect(nextFabExpanded(expanded, from, to)).toBe(next);
	});
});
