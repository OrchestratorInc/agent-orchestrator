import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({ platform: { OS: "ios" } }));
vi.mock("react-native", () => ({ Platform: fixtures.platform }));
vi.mock("./ThemeProvider", async () => {
	const { darkTheme } = await import("./theme");
	return { useTheme: () => darkTheme };
});

import { darkTheme, lightTheme } from "./theme";
import { pressFeedback, rippleColor, usePressFeedback, type PressFeedback, type PressKind } from "./pressFeedback";
import { stateOpacity, touchTarget } from "./tokens";

beforeEach(() => {
	fixtures.platform.OS = "ios";
});

describe("on iOS", () => {
	it("highlights a row's background with the hover surface", () => {
		expect(pressFeedback(darkTheme, "row").pressed).toEqual({ backgroundColor: darkTheme.bgElevatedHover });
		expect(pressFeedback(lightTheme, "row").pressed).toEqual({ backgroundColor: lightTheme.bgElevatedHover });
	});

	it.each<PressKind>(["button", "icon"])("fades a %s to the shared pressed opacity", (kind) => {
		expect(pressFeedback(darkTheme, kind).pressed).toEqual({ opacity: stateOpacity.pressed });
	});

	it("draws no ripple", () => {
		expect(pressFeedback(darkTheme, "row").android_ripple).toBeUndefined();
	});
});

describe("on Android", () => {
	beforeEach(() => {
		fixtures.platform.OS = "android";
	});

	it.each<PressKind>(["row", "button", "icon"])("lets the ripple carry a %s press alone, with no fade, fill or scale", (kind) => {
		expect(pressFeedback(darkTheme, kind).pressed).toBeUndefined();
	});

	it("bounds a row or button ripple to the view", () => {
		for (const kind of ["row", "button"] as const) {
			const ripple = pressFeedback(darkTheme, kind).android_ripple;
			expect(ripple).toMatchObject({ color: darkTheme.accentTint, borderless: false });
			expect(ripple?.radius).toBeUndefined();
		}
	});

	it("draws an icon ripple as an unbounded circle the size of the touch target", () => {
		expect(pressFeedback(darkTheme, "icon").android_ripple).toMatchObject({ borderless: true, radius: touchTarget / 2 });
		expect(pressFeedback(darkTheme, "icon", { radius: 20 }).android_ripple?.radius).toBe(20);
	});

	it("draws over the content only when asked", () => {
		expect(pressFeedback(darkTheme, "row").android_ripple?.foreground).toBeUndefined();
		expect(pressFeedback(darkTheme, "row", { foreground: true }).android_ripple?.foreground).toBe(true);
	});

	it("takes the ripple colour from the tone", () => {
		expect(pressFeedback(darkTheme, "button", { tone: "destructive" }).android_ripple?.color).toBe(darkTheme.tintRed);
		expect(pressFeedback(darkTheme, "button", { tone: "onAccent" }).android_ripple?.color).toBe(rippleColor(darkTheme, "onAccent"));
	});
});

describe("ripple colour", () => {
	it("uses the theme's tints", () => {
		expect(rippleColor(darkTheme)).toBe(darkTheme.accentTint);
		expect(rippleColor(darkTheme, "destructive")).toBe(darkTheme.tintRed);
	});

	it("draws ink on a filled accent in that fill's own ink, translucent", () => {
		// Dark mode's accent is near-white, where a white ripple would vanish.
		expect(rippleColor(darkTheme, "onAccent")).toMatch(new RegExp(`^${darkTheme.onAccent}[0-9a-f]{2}$`));
		expect(rippleColor(lightTheme, "onAccent")).toMatch(new RegExp(`^${lightTheme.onAccent}[0-9a-f]{2}$`));
	});
});

describe("usePressFeedback", () => {
	let renderer: ReactTestRenderer | undefined;
	afterEach(() => {
		act(() => renderer?.unmount());
		renderer = undefined;
		vi.unstubAllGlobals();
	});

	it("returns the same feedback across renders of one theme", () => {
		vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
		const seen: PressFeedback[] = [];
		function Probe({ tick }: { tick: number }) {
			seen.push(usePressFeedback("row", { tone: "neutral" }));
			return tick;
		}
		act(() => { renderer = create(<Probe tick={0} />); });
		act(() => renderer!.update(<Probe tick={1} />));
		expect(seen).toHaveLength(2);
		expect(seen[1]).toBe(seen[0]);
		expect(seen[0].pressed).toEqual({ backgroundColor: darkTheme.bgElevatedHover });
	});
});
