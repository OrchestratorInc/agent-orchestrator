import { existsSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

function read(relativePath: string): string {
	const path = fileURLToPath(new URL(relativePath, import.meta.url));
	return existsSync(path) ? readFileSync(path, "utf8") : "";
}

describe("Settings environment navigation", () => {
	it("pushes Environments horizontally inside the existing Settings sheet", () => {
		const rootLayout = read("../app/_layout.tsx");
		const settingsLayout = read("../app/settings/_layout.tsx");
		const settings = read("../app/settings/index.tsx");

		expect(settingsLayout).not.toBe("");
		expect(rootLayout).toMatch(/name="settings"[\s\S]*presentation:\s*"formSheet"/);
		expect(rootLayout).not.toMatch(/name="environments"/);
		expect(settingsLayout).toMatch(/name="index"[\s\S]*headerShown:\s*false/);
		expect(settingsLayout).toMatch(/name="environments"[\s\S]*animation:\s*"slide_from_right"/);
		expect(settingsLayout).toMatch(/name="environments"[\s\S]*gestureEnabled:\s*true/);
		expect(settings).toContain('label="Environments"');
		expect(settings).toContain('router.push("/settings/environments")');
		expect(settings).not.toContain('label="All machines"');
	});

	it("keeps Settings scroll gestures from dismissing the native sheet", () => {
		const rootLayout = read("../app/_layout.tsx");
		const settingsScreen = rootLayout.match(/<Stack\.Screen\s+name="settings"[\s\S]*?\/>/)?.[0] ?? "";

		expect(settingsScreen).not.toBe("");
		expect(settingsScreen).toMatch(/gestureEnabled:\s*false/);
		expect(settingsScreen).toMatch(/sheetGrabberVisible:\s*false/);
	});

	it("renders one stable in-sheet header below the grabber from the first frame", () => {
		const settingsLayout = read("../app/settings/_layout.tsx");
		const environments = read("../app/settings/environments.tsx");

		expect(environments).not.toBe("");
		expect(settingsLayout).toMatch(/name="environments"[\s\S]*headerShown:\s*false/);
		expect(environments).toContain("<View style={styles.header}>");
		expect(environments).toContain('<NativeHeaderButton icon="back"');
		expect(environments).toContain('<NativeHeaderButton icon="add"');
		expect(environments).toContain('<Text style={styles.headerTitle}>Environments</Text>');
		expect(environments).not.toContain("styles.introTitle");
		expect(environments).toMatch(/header:\s*\{\s*height:\s*88/);
		expect(environments).toMatch(/headerButton:\s*\{\s*position:\s*"absolute"[^}]*width:\s*44[^}]*height:\s*44[^}]*borderRadius:\s*22/s);
	});

	it("clips scrolling machine cards below the opaque header boundary", () => {
		const environments = read("../app/settings/environments.tsx");

		expect(environments).toContain("<View style={styles.scrollViewport}>");
		expect(environments).toMatch(/scrollViewport:\s*\{[^}]*overflow:\s*"hidden"/s);
		expect(environments).toMatch(/header:\s*\{[^}]*backgroundColor:\s*t\.bgBase[^}]*zIndex:\s*1/s);
	});
});
