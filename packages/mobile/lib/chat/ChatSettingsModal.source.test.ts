import { existsSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// Pins the sheet's wiring from its source, as ChatSettingsModelIcon.source.test.ts
// does; the decisions it delegates are tested in turnSettingsModel.test.ts.
const sheet = readFileSync(new URL("./ChatSettingsModal.tsx", import.meta.url), "utf8");

describe("turn settings sheet", () => {
	it("is one sheet for both platforms", () => {
		expect(existsSync(fileURLToPath(new URL("./ChatSettingsModal.android.tsx", import.meta.url)))).toBe(false);
	});

	it("places the effort slider without clamping an effort that is none of its levels", () => {
		expect(sheet).toContain("const selectedIndex = effortSliderIndex(choices, selected);");
		expect(sheet).toContain("const next = effortSliderWrite(choices, selected, nextIndex);");
		expect(sheet).not.toMatch(/Math\.max\(0,\s*(?:choices\.findIndex|effortSliderIndex)/);
	});

	it("names a native model with the helper the turn-settings menu uses", () => {
		expect(sheet).toContain("nativeModelLabel(selected, snapshot.settings.model)");
	});

	it("hands the effort slider the route's save so it can go back after a rejection", () => {
		const route = readFileSync(new URL("../../app/sheets/chat-settings.tsx", import.meta.url), "utf8");
		expect(route).toContain("onRefresh={refresh} onSettings={changeSettings} onOption={changeOption}");
		expect(sheet).toContain("? onOption(effortOption.id, { value: reasoningEffort })\n\t\t\t\t\t: onSettings({ ...snapshot.settings, reasoningEffort })");
		expect(sheet).toContain("onChangeRef.current(next).then(settle, settle);");
	});

	// No hand-drawn radio rows, page of choices, uppercase titles or RN Switch:
	// the platforms' own pickers, switches and grouped sections.
	it("builds the form from native controls", () => {
		expect(sheet).toContain('<FieldGroup modifiers={[scrollContentBackground("hidden"), refreshable(onRefresh)]}>');
		expect(sheet).toContain('modifiers={[pickerStyle("inline"), labelsHidden(), disabledModifier(Boolean(disabled))]}');
		expect(sheet).toContain("<RadioButton selected={choice.value === selected} />");
		expect(sheet).toContain("<Switch label={option.name}");
		for (const gone of ["ChoicePage", "SettingsGroup", 'accessibilityRole="radio"', "RESPONSE", "PERMISSIONS", "ADVANCED", "../icons", 'from "react-native";\nimport { Switch']) {
			expect(sheet).not.toContain(gone);
		}
		expect(sheet).not.toMatch(/import \{[^}]*\bSwitch\b[^}]*\} from "react-native"/);
	});

	// A clickable inside FieldGroup.Section's row covered the text only; the
	// Surface is the whole row and carries the disabled state to TalkBack.
	it("makes each Android choice row one tap target", () => {
		expect(sheet).toContain("<Surface onClick={() => setExpanded(true)} enabled={!disabled}");
		expect(sheet).not.toContain("clickable(");
	});
});
