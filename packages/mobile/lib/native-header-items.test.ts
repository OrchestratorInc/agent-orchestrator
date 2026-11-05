import { describe, expect, it, vi } from "vitest";

// iOS 26 draws a shared glass background behind every bar-button item. Our
// controls are the material themselves, so the two stacked and the button came
// out with a second, squarer outline — the whole reason this helper exists.
let platformOS = "ios";
vi.mock("react-native", () => ({
	Platform: {
		get OS() {
			return platformOS;
		},
	},
}));

const { glassHeaderControl } = await import("./native-header-items");

const control = { type: "element" } as never;

describe("glassHeaderControl", () => {
	it("declares an iOS control as an item that hides the shared background", () => {
		platformOS = "ios";
		const options = glassHeaderControl("right", control);
		expect(options.headerRight).toBeUndefined();
		expect(options.unstable_headerRightItems?.({} as never)).toEqual([
			{ type: "custom", element: control, hidesSharedBackground: true },
		]);
	});

	// The leading side belongs to the system back button, which keeps its
	// back-history menu on iOS and its arrow, ripple and 48dp target on Android.
	it("never touches the back button", () => {
		for (const os of ["ios", "android"]) {
			platformOS = os;
			const options = glassHeaderControl("right", control);
			expect(options).not.toHaveProperty("headerBackVisible");
			expect(options).not.toHaveProperty("headerLeft");
			expect(options).not.toHaveProperty("unstable_headerLeftItems");
		}
	});

	it("clears the item list when vacating a slot", () => {
		// An empty array is a list that renders nothing and outranks
		// `headerRight`, so a screen that swaps its glass control for a plain one
		// (the terminal's browser toggles) would be left with a blank bar.
		platformOS = "ios";
		const options = glassHeaderControl("right");
		expect(options.unstable_headerRightItems).toBeUndefined();
		expect(options.headerRight).toBeUndefined();
	});

	it("keeps the plain slot on Android, which has no shared background", () => {
		platformOS = "android";
		const right = glassHeaderControl("right", control);
		expect(right.headerRight?.({} as never)).toBe(control);
		expect(right.unstable_headerRightItems).toBeUndefined();
		expect(glassHeaderControl("right").headerRight).toBeUndefined();
	});
});
