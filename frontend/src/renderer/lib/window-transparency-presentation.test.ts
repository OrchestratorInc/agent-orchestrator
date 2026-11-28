import { describe, expect, it } from "vitest";
import { windowTransparencyPresentation, type WindowTransparencyView } from "./window-transparency-presentation";

describe("window transparency presentation", () => {
	it.each([
		[{ supported: null, enabled: false, reason: null }, false, "settings.windowTranslucencyHint"],
		[{ supported: true, enabled: false, reason: null }, false, "settings.windowTranslucencyHint"],
		[{ supported: true, enabled: true, reason: null }, false, "settings.windowTranslucencyHint"],
		[{ supported: false, enabled: false, reason: "unsupported" }, true, "settings.windowTranslucencyUnsupported"],
		[{ supported: null, enabled: false, reason: "error" }, false, "settings.windowTranslucencyError"],
		[{ supported: false, enabled: false, reason: "error" }, false, "settings.windowTranslucencyError"],
		[{ supported: true, enabled: false, reason: "system" }, false, "settings.windowTranslucencySystem"],
	] as const)("keeps Windows discoverable for %j", (state, disabled, hint) => {
		expect(windowTransparencyPresentation(state, { mac: false, windows: true })).toEqual({ visible: true, disabled, hint });
	});
	it("shows macOS during startup and errors, and never offers the setting on Linux", () => {
		for (const reason of [null, "error"] as const) {
			const state: WindowTransparencyView = { supported: null, enabled: false, reason };
			expect(windowTransparencyPresentation(state, { mac: true, windows: false })).toMatchObject({ visible: true, disabled: false });
			expect(windowTransparencyPresentation(state, { mac: false, windows: false }).visible).toBe(false);
		}
	});
});
