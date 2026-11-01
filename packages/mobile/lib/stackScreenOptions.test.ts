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
	it("draws the tab roots' titles large, heavy in the system face, without a hairline", () => {
		expect(largeTitleScreenOptions()).toMatchObject({
			headerLargeTitleEnabled: true,
			headerLargeTitleShadowVisible: false,
			headerLargeTitleStyle: { fontWeight: "800" },
		});
	});

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

	// The app's family carries Android's title rather than Roboto at an iOS weight.
	it("titles Android's top app bar in the app's face at Material's title size", () => {
		platform.OS = "android";
		expect(stackScreenOptions(theme).headerTitleStyle).toEqual({ fontFamily: "DMSans_600SemiBold", fontSize: 20 });
	});
});
