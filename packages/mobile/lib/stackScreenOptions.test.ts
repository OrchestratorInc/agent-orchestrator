import { beforeEach, describe, expect, it, vi } from "vitest";

const platform = vi.hoisted(() => ({ OS: "ios", Version: "18.5" as string | number }));
vi.mock("react-native", () => ({ Platform: platform }));

import { stackScreenOptions, untitledTabScreenOptions } from "./stackScreenOptions";
import type { Theme } from "./theme";

const theme = new Proxy({}, { get: (_target, property) => `#${String(property)}` }) as Theme;

beforeEach(() => {
	platform.OS = "ios";
	platform.Version = "18.5";
});

describe("board tab header", () => {
	// The tabs' roots show only their bar items; the title stays for VoiceOver.
	it("draws no title in the bar", () => {
		expect(untitledTabScreenOptions()).toMatchObject({ headerTitle: "" });
		expect(untitledTabScreenOptions()).not.toHaveProperty("headerLargeTitleEnabled");
	});

	it("keeps the page-coloured bar before iOS 26", () => {
		const options = { ...stackScreenOptions(theme), ...untitledTabScreenOptions() };
		expect(options.headerStyle).toEqual({ backgroundColor: "#bgBase" });
		expect(options.headerTransparent).toBeUndefined();
	});

	// iOS 26's bar is clear glass, so the list scrolls on under it.
	it("clears the bar on iOS 26", () => {
		platform.Version = "26.0";
		const options = { ...stackScreenOptions(theme), ...untitledTabScreenOptions() };
		expect(options.headerStyle).toBeUndefined();
		expect(options.headerTransparent).toBe(true);
	});

	it("leaves Android's top app bar page-coloured, untitled", () => {
		platform.OS = "android";
		expect(untitledTabScreenOptions()).toEqual({ headerTitle: "" });
	});
});
