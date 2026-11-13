import { beforeEach, describe, expect, it, vi } from "vitest";

const platform = vi.hoisted(() => ({ OS: "ios", Version: "18.5" as string | number }));
vi.mock("react-native", () => ({ Platform: platform }));

import { largeTitleScreenOptions, stackScreenOptions } from "./stackScreenOptions";
import type { Theme } from "./theme";

const theme = new Proxy({}, { get: (_target, property) => `#${String(property)}` }) as Theme;

beforeEach(() => {
	platform.OS = "ios";
	platform.Version = "18.5";
});

describe("large-title header", () => {
	// Before iOS 26 nothing keeps the collapsed title legible over the list, so the
	// bar keeps the page's colour from the stack defaults.
	it("keeps the page-coloured bar before iOS 26", () => {
		const options = { ...stackScreenOptions(theme), ...largeTitleScreenOptions() };
		expect(options.headerStyle).toEqual({ backgroundColor: "#bgBase" });
	});

	// iOS 26 hides a large title drawn over a set background.
	it("clears the bar's background on iOS 26", () => {
		platform.Version = "26.0";
		const large = largeTitleScreenOptions();
		expect(large).toHaveProperty("headerStyle", undefined);
		expect({ ...stackScreenOptions(theme), ...large }.headerStyle).toBeUndefined();
	});

	it("leaves Android's top app bar as the stack draws it", () => {
		platform.OS = "android";
		expect(largeTitleScreenOptions()).toEqual({});
	});
});

describe("push transition", () => {
	// Android's "default" was a short fade that read as the page appearing in
	// place; it slides in as iOS's push does.
	it("slides pages in from the right on Android", () => {
		platform.OS = "android";
		expect(stackScreenOptions(theme).animation).toBe("ios_from_right");
	});

	it("leaves iOS on UIKit's own push", () => {
		expect(stackScreenOptions(theme)).not.toHaveProperty("animation");
	});
});
