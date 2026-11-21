import { beforeEach, describe, expect, it, vi } from "vitest";

const platform = vi.hoisted(() => ({ OS: "ios", Version: "18.5" as string | number }));
vi.mock("./FlatBackButton", async () => {
	const React = await import("react");
	return { FlatBackButton: (props: object) => React.createElement("FlatBackButton", props) };
});
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

describe("back button", () => {
	// Every page's back is the app's flat chevron, as the conversation draws it,
	// not the system's glass-circled one: the system's is hidden and the
	// chevron is a header item with the shared glass background off.
	it("puts the flat chevron where the stack can go back, without glass", () => {
		const options = stackScreenOptions(theme);
		expect(options.headerBackVisible).toBe(false);
		const items = options.unstable_headerLeftItems!({ canGoBack: true });
		expect(items).toHaveLength(1);
		expect(items[0]).toMatchObject({ type: "custom", hidesSharedBackground: true });
		expect(options.unstable_headerLeftItems!({ canGoBack: false })).toEqual([]);
	});
});
