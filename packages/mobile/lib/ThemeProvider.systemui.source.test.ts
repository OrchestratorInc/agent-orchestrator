import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const provider = readFileSync(fileURLToPath(new URL("./ThemeProvider.tsx", import.meta.url)), "utf8");

// The two pieces of system chrome that sit *under* the React tree, so nothing a
// screen draws can reach them. Both are native modules, so both are asked for at
// runtime rather than imported: importing a missing one throws while the module
// is being evaluated and takes the app down before it renders.
describe("system chrome follows the palette", () => {
	it("tints the window from the palette", () => {
		expect(provider).toContain('requireOptionalNativeModule<{ setBackgroundColorAsync(color: string): Promise<void> }>("ExpoSystemUI")');
		expect(provider).toContain("systemUI.setBackgroundColorAsync(backgroundColor)");
	});

	it("sets Android's navigation bar from the palette, not from the device theme", () => {
		expect(provider).toContain('requireOptionalNativeModule<{ setStyle(style: "light" | "dark"): Promise<void> }>("ExpoNavigationBar")');
		expect(provider).toContain('navigationBar?.setStyle(scheme === "dark" ? "light" : "dark")');
	});

	it("re-applies Android's status bar icons on every scheme change and whenever the app comes forward", () => {
		expect(provider).toContain('StatusBar.setBarStyle(scheme === "dark" ? "light-content" : "dark-content")');
		expect(provider).toMatch(/AppState\.addEventListener\("change", \(state\) => \{\s*if \(state === "active"\) applySystemBars\(\);/);
		expect(provider).toContain("return () => subscription.remove();");
	});

	it("keeps both behind a platform guard and a missing-module guard", () => {
		expect(provider).toContain('if (Platform.OS !== "android") return;');
		expect(provider).toContain("?.setStyle(");
	});
});
