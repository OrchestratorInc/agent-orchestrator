import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const ios = readFileSync(new URL("./ChatTurnSettingsControl.ios.tsx", import.meta.url), "utf8");
const android = readFileSync(new URL("./chatMenuIcons.android.ts", import.meta.url), "utf8");
const sheet = readFileSync(new URL("./ChatSettingsModal.tsx", import.meta.url), "utf8");

// The turn-settings menus mark each setting with a glyph; the sheet is a native
// form whose section titles name the setting, with no glyph of their own.
describe("model setting icon", () => {
	it("uses layers for the model selector on both platforms", () => {
		expect(ios).toContain('model: "square.stack.3d.up"');
		expect(android).toContain('model: require("../../assets/icons/stacks.xml")');
	});

	it("leaves the sheet's sections without glyphs", () => {
		expect(sheet).not.toContain("AppSymbol name=\"model\"");
		expect(sheet).toContain('<ChoiceSection key={`model:${revision}`} title="Model"');
	});
});

describe("approvals setting icon", () => {
	it("uses the dashed check circle artwork on iOS and the shield symbol on Android", () => {
		expect(ios).toContain('<Image assetName="CircleDashedCheck" />');
		expect(android).toContain('permissions: require("../../assets/icons/verified_user.xml")');
	});
});
