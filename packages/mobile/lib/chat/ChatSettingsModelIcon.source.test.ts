import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const ios = readFileSync(new URL("./ChatTurnSettingsControl.ios.tsx", import.meta.url), "utf8");
const menuIcons = readFileSync(new URL("./chatMenuIcons.types.ts", import.meta.url), "utf8");
const sheet = readFileSync(new URL("./ChatSettingsModal.tsx", import.meta.url), "utf8");

// The turn-settings menus mark each setting with a glyph; the sheet is a native
// form whose section titles name the setting, with no glyph of their own.
describe("model setting icon", () => {
	it("uses layers for the model selector on both platforms", () => {
		expect(ios).toContain('model: "square.stack.3d.up"');
		expect(menuIcons).toContain('model: { ios: "square.stack.3d.up", android: "stacks" }');
	});

	it("leaves the sheet's sections without glyphs", () => {
		expect(sheet).not.toContain("AppSymbol name=\"model\"");
		expect(sheet).toContain('<ChoiceSection key={`model:${revision}`} title="Model"');
	});
});

describe("approvals setting icon", () => {
	it("uses the dashed check circle artwork on iOS and the shield symbol on Android", () => {
		expect(ios).toContain('<Image assetName="CircleDashedCheck" modifiers={[imageScale("small")]} />');
		expect(menuIcons).toContain('permissions: { ios: "checkmark.shield", android: "verified_user" }');
	});
});
