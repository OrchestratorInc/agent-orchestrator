import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("..", import.meta.url));

function sources(dir: string): string[] {
	return readdirSync(join(root, dir)).flatMap((name) => {
		const path = join(dir, name);
		if (statSync(join(root, path)).isDirectory()) return sources(path);
		return /\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name) ? [path] : [];
	});
}

function source(path: string): string {
	return readFileSync(join(root, path), "utf8");
}

describe("native hosts take the platform's own theme", () => {
	// The accent is hue-less. Seeding a Host with it turned every Material control
	// grey and overrode Material You on Android, and gave iOS a near-white or
	// near-black tint. A control that needs a colour names it on the control.
	it("never seeds a Host", () => {
		const seeded = [...sources("app"), ...sources("lib")].filter((path) => source(path).includes("seedColor"));
		expect(seeded).toEqual([]);
	});

	// Neither the switch nor the form around it carries a tint, which the switch would take up.
	it("leaves the settings switches to the system's own on colour", () => {
		const settingsForm = source("lib/SettingsForm.tsx");
		const toggleRow = settingsForm.match(/export function SettingsToggleRow[\s\S]*?\n}\n/)?.[0] ?? "";
		const form = settingsForm.match(/export function SettingsForm\b[\s\S]*?\n}\n/)?.[0] ?? "";
		expect(toggleRow).toContain("<Toggle ");
		expect(form).toContain("<FieldGroup");
		for (const part of [toggleRow, form]) expect(part).not.toContain("tint(");
	});

	it("tints the iOS settings menus like the values in the rows around them", () => {
		expect(source("lib/SettingsForm.tsx")).toMatch(/modifiers=\{\[pickerStyle\("menu"\), tint\(t\.textSecondary\)/);
	});

	it("gives native text fields the caret colour of the app's React Native fields", () => {
		expect(source("lib/chat/elicitation-native-controls.ios.tsx")).toMatch(/<TextField[\s\S]*?modifiers=\{\[[\s\S]*?tint\(t\.highlight\)[\s\S]*?\]\}/);
	});
});
