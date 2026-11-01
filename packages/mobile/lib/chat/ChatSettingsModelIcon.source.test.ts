import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const shared = readFileSync(new URL("../model-controls.ios.tsx", import.meta.url), "utf8");
const android = readFileSync(new URL("./ChatTurnSettingsControl.tsx", import.meta.url), "utf8");
const menuIcons = readFileSync(new URL("./chatMenuIcons.types.ts", import.meta.url), "utf8");

// The effort is the custom speedometer on both platforms, lit to the chosen
// level; the menu icon names it in any other list.
describe("effort setting icon", () => {
	it("draws the effort as a speedometer", () => {
		expect(shared).not.toContain("EffortMenu");
		expect(android).toContain('<EffortGaugeButton choices={effort.choices} open={effort.open} onPress={effort.onToggle} disabled={off} testID="chat-effort" />');
		expect(menuIcons).toContain('effort: { ios: "gauge.medium", android: "speed" }');
	});
});

describe("approvals setting icon", () => {
	// Each mode its own symbol on iOS; the shield on Android.
	it("draws the mode's own symbol on iOS and the shield symbol on Android", () => {
		expect(shared).toContain('label={<SymbolFace name={selected?.symbol ?? "checkmark.shield"} />}');
		expect(android).toContain('const approvalGlyph = { ios: "checkmark.shield", android: "verified_user" } as const;');
	});
});
