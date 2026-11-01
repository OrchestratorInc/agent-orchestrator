import { existsSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

function read(relativePath: string): string {
	const path = fileURLToPath(new URL(relativePath, import.meta.url));
	return existsSync(path) ? readFileSync(path, "utf8") : "";
}

describe("Settings environment navigation", () => {
	// Settings is the list of machines; there is no second list page.
	it("lists the machines in Settings itself, with no list page of their own", () => {
		const settingsLayout = read("../app/settings/_layout.tsx");
		const settings = read("../app/settings/index.tsx");

		expect(settingsLayout).not.toBe("");
		expect(settingsLayout).not.toContain('name="environments"');
		expect(read("../app/settings/environments.tsx")).toBe("");
		expect(settings).not.toContain('label="Paired machines"');
		expect(settings).not.toContain("/settings/environments");
	});

	// A cold link to /settings/theme lands with the Settings list beneath it,
	// so the back button goes back to the list instead of leaving Settings.
	// On iOS, where Settings is pushed from the board. Android's Settings list
	// is a tab, so a page opens alone over it and back returns to the tab.
	it("opens every Settings page above the Settings list on iOS", () => {
		const settingsLayout = read("../app/settings/_layout.tsx");

		expect(settingsLayout).toContain('export const unstable_settings = Platform.OS === "ios" ? { initialRouteName: "index" } : {};');
	});

	// The pages' titles, back buttons and actions are the native header's, with
	// the same tint and type as every other stack in the app. On iOS 26 the bar
	// is the system's glass rather than a painted one.
	it("gives every Settings page the native header", () => {
		const settingsLayout = read("../app/settings/_layout.tsx");

		expect(settingsLayout).toContain("<Stack screenOptions={{ ...stackScreenOptions(t), ...(SYSTEM_GLASS_HEADER ? { headerTransparent: true, headerStyle: undefined } : null) }}>");
		expect(settingsLayout).toContain('<Stack.Screen name="index" options={{ title: "Settings" }} />');
		expect(settingsLayout).toContain('<Stack.Screen name="theme" options={{ title: "Theme" }} />');
		expect(settingsLayout).not.toContain("headerShown");
		// Every page pushes and goes back like any other: no transition or
		// gesture of its own.
		expect(settingsLayout).not.toContain("animation:");
		expect(settingsLayout).not.toContain("fullScreenGestureEnabled");
	});

	// No page draws a bar of its own: no fixed-height header row, no absolutely
	// positioned back button, no title text standing in for the bar's.
	it("draws no header rows of its own", () => {
		for (const page of ["../app/settings/index.tsx", "../app/settings/theme.tsx", "../app/settings/machine/[id]/index.tsx"]) {
			const text = read(page);
			expect(text).not.toBe("");
			expect(text).not.toContain("NativeHeaderButton");
			expect(text).not.toContain("<View style={styles.header}>");
			expect(text).not.toMatch(/header:\s*\{\s*height:/);
			expect(text).not.toContain("styles.headerTitle");
		}
	});

	// Settings is a pushed page on both platforms, so it leaves by the bar's
	// back button, never a close item of its own.
	it("leaves Settings by the back button", () => {
		const settings = read("../app/settings/index.tsx");

		expect(settings).not.toContain("Close settings");
		expect(settings).not.toContain("Stack.Toolbar");
	});

	// Each machine's row in Settings opens its page, pushed inside Settings'
	// stack with its editor above it.
	it("pushes a machine's page and its editor inside Settings' own stack", () => {
		const settings = read("../app/settings/index.tsx");
		const settingsLayout = read("../app/settings/_layout.tsx");

		expect(settings).toContain("<MachineRow");
		expect(settings).toContain('router.push({ pathname: "/settings/machine/[id]", params: { id: host.id } })');
		expect(settingsLayout).toContain('<Stack.Screen name="machine/[id]/index" options={{ title: "Machine" }} />');
		expect(settingsLayout).toContain('<Stack.Screen name="machine/[id]/edit" options={{ title: "Edit machine" }} />');
	});
});
