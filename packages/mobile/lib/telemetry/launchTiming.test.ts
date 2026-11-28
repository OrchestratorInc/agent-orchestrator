import { describe, expect, it } from "vitest";
import { launchKind, launchProperties } from "./launchTiming";

describe("launchKind", () => {
	it("tells a first launch, an update and a plain cold launch apart", () => {
		expect(launchKind(null, "2.4.0|embedded", false)).toBe("first");
		expect(launchKind(null, "2.4.0|embedded", true)).toBe("update");
		expect(launchKind("2.4.0|embedded", "2.4.0|embedded", true)).toBe("cold");
		expect(launchKind("2.4.0|embedded", "2.4.0|0198a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", true)).toBe("update");
	});
});

describe("launchProperties", () => {
	const marks = { startTime: 1_000, initializeRuntimeStart: 1_200, executeJavaScriptBundleEntryPointStart: 1_450 };

	it("times each step from the app's start", () => {
		expect(launchProperties({ kind: "cold", outcome: "usable", marks, visibleAt: 2_100, usableAt: 3_800, landingSurface: "board" })).toEqual({
			launch_kind: "cold",
			outcome: "usable",
			landing_surface: "board",
			clock_origin: "process",
			to_js_start_ms: 450,
			to_visible_ms: 1_100,
			to_usable_ms: 2_800,
		});
	});

	// What an Expo Release build reports: React Native copies the runtime's start.
	it("does not call a copied runtime start the app's", () => {
		const props = launchProperties({ kind: "cold", outcome: "usable", marks: { startTime: 1_200, initializeRuntimeStart: 1_200, executeJavaScriptBundleEntryPointStart: 1_260 }, visibleAt: 1_444 });
		expect(props).toMatchObject({ clock_origin: "runtime", to_visible_ms: 244 });
		// Measured from the runtime's own start, the JS step says nothing.
		expect(props).not.toHaveProperty("to_js_start_ms");
	});

	it("falls back to the runtime's start when the app's is not recorded", () => {
		const props = launchProperties({ kind: "cold", outcome: "usable", marks: { initializeRuntimeStart: 1_200 }, visibleAt: 2_200, usableAt: 3_200 });
		expect(props).toMatchObject({ clock_origin: "runtime", to_visible_ms: 1_000, to_usable_ms: 2_000 });
	});

	it("leaves out the usable time when the launch never showed live data", () => {
		const props = launchProperties({ kind: "first", outcome: "timeout", marks, visibleAt: 2_100, usableAt: 3_000, landingSurface: "onboarding" });
		expect(props).toMatchObject({ outcome: "timeout", to_visible_ms: 1_100, landing_surface: "onboarding" });
		expect(props).not.toHaveProperty("to_usable_ms");
	});

	it("keeps the first frame for a phone with nowhere to load from", () => {
		const props = launchProperties({ kind: "first", outcome: "no_machine", marks, visibleAt: 2_100, landingSurface: "onboarding" });
		expect(props).toMatchObject({ outcome: "no_machine", to_visible_ms: 1_100, landing_surface: "onboarding" });
		expect(props).not.toHaveProperty("to_usable_ms");
	});

	it("sends only the kind when the app went to the background first", () => {
		expect(launchProperties({ kind: "cold", outcome: "backgrounded", marks, visibleAt: 2_100 })).toEqual({ launch_kind: "cold", outcome: "backgrounded" });
	});

	it("drops times from a mismatched clock", () => {
		const props = launchProperties({ kind: "cold", outcome: "usable", marks: { startTime: 5_000 }, visibleAt: 4_000, usableAt: 5_000 + 11 * 60_000 });
		expect(props).toEqual({ launch_kind: "cold", outcome: "usable", clock_origin: "process" });
	});

	it("sends no times without startup marks", () => {
		expect(launchProperties({ kind: "cold", outcome: "usable", marks: null, visibleAt: 10, usableAt: 20 })).toEqual({ launch_kind: "cold", outcome: "usable" });
	});
});
