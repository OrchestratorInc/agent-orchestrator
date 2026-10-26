import { existsSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

function read(relativePath: string): string {
	const path = fileURLToPath(new URL(relativePath, import.meta.url));
	return existsSync(path) ? readFileSync(path, "utf8") : "";
}

describe("Settings environment navigation", () => {
	it("pushes Environments inside Settings' own stack", () => {
		const rootLayout = read("../app/_layout.tsx");
		const settingsLayout = read("../app/settings/_layout.tsx");
		const settings = read("../app/settings/index.tsx");

		expect(settingsLayout).not.toBe("");
		expect(rootLayout).not.toMatch(/name="environments"/);
		expect(settingsLayout).toContain('<Stack.Screen name="environments" options={{ title: "Environments", fullScreenGestureEnabled: true }} />');
		expect(settings).toContain('label="Environments"');
		expect(settings).toContain('router.push("/settings/environments")');
		expect(settings).not.toContain('label="All machines"');
	});

	// A cold link to /settings/theme lands with the Settings list beneath it,
	// so the back button goes back to the list instead of closing Settings.
	it("opens every Settings page above the Settings list", () => {
		const settingsLayout = read("../app/settings/_layout.tsx");

		expect(settingsLayout).toContain('export const unstable_settings = { initialRouteName: "index" };');
	});

	// The pages' titles, back buttons and actions are the native header's, with
	// the same tint, type and bar as every other stack in the app.
	it("gives every Settings page the native header", () => {
		const settingsLayout = read("../app/settings/_layout.tsx");

		expect(settingsLayout).toContain("<Stack screenOptions={stackScreenOptions(t)}>");
		expect(settingsLayout).toContain('<Stack.Screen name="index" options={{ title: "Settings" }} />');
		expect(settingsLayout).toContain('<Stack.Screen name="theme" options={{ title: "Theme", fullScreenGestureEnabled: true }} />');
		expect(settingsLayout).not.toContain("headerShown");
		expect(settingsLayout).not.toContain("animation:");
	});

	// No page draws a bar of its own: no fixed-height header row, no absolutely
	// positioned back button, no title text standing in for the bar's.
	it("draws no header rows of its own", () => {
		for (const page of ["../app/settings/index.tsx", "../app/settings/theme.tsx", "../app/settings/environments.tsx"]) {
			const text = read(page);
			expect(text).not.toBe("");
			expect(text).not.toContain("NativeHeaderButton");
			expect(text).not.toContain("<View style={styles.header}>");
			expect(text).not.toMatch(/header:\s*\{\s*height:/);
			expect(text).not.toContain("styles.headerTitle");
		}
	});

	it("closes the iOS Settings sheet from its bar and adds machines from Environments' bar", () => {
		const settings = read("../app/settings/index.tsx");
		const environments = read("../app/settings/environments.tsx");

		expect(settings).toContain('const headerItems = useMemo(() => Platform.OS === "ios" ? (');
		expect(settings).toContain('<Stack.Toolbar.Button icon={toolbarIcon("close")} accessibilityLabel="Close settings" onPress={() => backOr(router)} />');
		expect(environments).toContain('<Stack.Toolbar.Button icon={toolbarIcon("add")} accessibilityLabel="Add machine" onPress={() => router.push("/pair")} />');
	});

	it("clips scrolling machine cards to the page below the native header", () => {
		const environments = read("../app/settings/environments.tsx");

		expect(environments).toContain("<View style={styles.scrollViewport}>");
		expect(environments).toMatch(/scrollViewport:\s*\{[^}]*overflow:\s*"hidden"/s);
	});
});
