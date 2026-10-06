import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

// Pins the Android sheet's wiring from its source, as ChatSettingsModelIcon.source.test.ts
// does; the decisions it delegates are tested in turnSettingsModel.test.ts.
const android = readFileSync(new URL("./ChatSettingsModal.android.tsx", import.meta.url), "utf8");

describe("Android turn settings sheet", () => {
	it("places the effort slider without clamping an effort that is none of its levels", () => {
		expect(android).toContain("const selectedIndex = effortSliderIndex(choices, selected);");
		expect(android).toContain("const next = effortSliderWrite(choices, selected, nextIndex);");
		expect(android).not.toMatch(/Math\.max\(0,\s*(?:choices\.findIndex|effortSliderIndex)/);
	});

	it("names a native model with the helper the iOS row uses", () => {
		expect(android).toContain("nativeModelLabel(selected, snapshot.settings.model)");
	});

	it("hands the effort slider the route's save so it can go back after a rejection", () => {
		// Typecheck resolves the route's import to the iOS sheet only, so this pins
		// that the route still passes the promise through to the Android sheet.
		const route = readFileSync(new URL("../../app/sheets/chat-settings.tsx", import.meta.url), "utf8");
		expect(route).toContain("onSettings={changeSettings} onOption={changeOption}");
		expect(android).toContain("? onOption(effortOption.id, { value: reasoningEffort })\n\t\t\t\t\t: onSettings({ ...snapshot.settings, reasoningEffort })");
		expect(android).toContain("onChangeRef.current(next).then(settle, settle);");
	});
});
