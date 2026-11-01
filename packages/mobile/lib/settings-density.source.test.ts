import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const read = (path: string) => readFileSync(fileURLToPath(new URL(path, import.meta.url)), "utf8");

const settings = read("../app/settings/index.tsx");
const ios = read("./SettingsForm.tsx");
const android = read("./SettingsForm.android.tsx");

// Settings is the platform's own form now: SwiftUI's Form on iOS, a Material 3
// list on Android. Row height, type, separators, press feedback and roles come
// from those, so the page sets none of them itself.
describe("settings screen", () => {
	it("renders from the already-resolved app config without a second storage loader", () => {
		expect(settings).toMatch(/const \{ config,[^}]*reloadConfig \} = useApp\(\);/);
		expect(settings).not.toMatch(/\bloadConfig\(\)/);
	});

	it("is built from the native settings rows, with no hand-drawn cards, rows or separators", () => {
		expect(settings).toContain("<SettingsForm");
		expect(settings).toContain("<SettingsSection");
		for (const handBuilt of ["Pressable", "ScrollView", "SettingsCard", "CardRow", "hairlineWidth", "borderRadius", "minHeight", "fontSize", "lucide", "./icons", "Feather"]) {
			expect(settings).not.toContain(handBuilt);
		}
	});

	it("draws Software update and Sign out as button rows, Sign out destructive", () => {
		expect(settings).toMatch(/<SettingsButtonRow\s+icon="update"\s+label="Software update"/);
		expect(settings).toMatch(/<SettingsButtonRow icon="signOut" label="Sign out" destructive /);
	});

	it("scans a new code with the QR scanner glyph", () => {
		expect(settings).toMatch(/icon="scan"\s+label="Scan new code"/);
		expect(read("./SettingsForm.types.ts")).toContain("scan: symbols.qrScanner,");
	});

	// The version line was 11pt at 3:1 contrast; it is a section footer now, in
	// the platform's footnote style.
	it("puts the version in the Support footer instead of an 11pt caption", () => {
		expect(settings).toContain('<SettingsSection title="Support" footer={`Orchestrator ${formatVersionLine(buildInfo())}`}>');
		expect(settings).not.toContain("caption2");
	});
});

describe("the iOS settings form", () => {
	it("is SwiftUI's Form through the universal FieldGroup, in the app's family at a Dynamic Type style", () => {
		expect(ios).toContain('import { FieldGroup, Host, ListItem } from "@expo/ui";');
		expect(ios).toContain("<FieldGroup.Section");
		// Geist by its PostScript name at a ramp step, scaled by that step's text style.
		expect(ios).toContain('appFont("body")');
		const fonts = ios.match(/font\(\{[^}]*\}\)/g) ?? [];
		for (const call of fonts) {
			// Text scales with its text style, never at a bare point size.
			if (/\bsize:/.test(call)) expect(call).toMatch(/\btextStyle:/);
			expect(call).not.toMatch(/\bfamily:/);
		}
	});

	it("uses the native controls: Toggle, menu and inline Pickers, and a role-carrying Button", () => {
		expect(ios).toContain("<Toggle ");
		expect(ios).toContain('pickerStyle("menu")');
		expect(ios).toContain('pickerStyle("inline")');
		// A status row's action is a real bordered button, which the system greys out.
		expect(ios).toContain('buttonStyle("bordered")');
		expect(ios).toContain("disabledModifier(actionDisabled || actionLoading)");
		expect(ios).toContain('role={destructive ? "destructive" : undefined}');
	});
});

describe("the Android settings form", () => {
	it("is a Compose lazy list of Material ListItems, rows pressed as a whole", () => {
		expect(android).toContain("<LazyColumn");
		expect(android).toContain("<ListItem");
		expect(android).toContain("clickable(onPress)");
		expect(android).toContain('toggleable(value, () => onValueChange(!value), { role: "switch" })');
		expect(android).toContain('"radioButton"');
		expect(android).toContain("<SingleChoiceSegmentedButtonRow");
		// A status row's action, and a menu picker's current choice, are
		// Material's outlined button; the action greys out when it can't run.
		expect(android).toContain("<OutlinedButton");
		expect(android).toContain("enabled={!actionDisabled && !actionLoading}");
	});

	it("takes its sizes from the tokens and its text from the Material type scale", () => {
		expect(android).not.toMatch(/fontSize:/);
		expect(android).not.toMatch(/\b(?:radius|spacedBy):\s*\d/);
		expect(android).toContain('typography: "bodyLarge"');
		expect(android).toContain("stateOpacity.disabled");
	});
});
