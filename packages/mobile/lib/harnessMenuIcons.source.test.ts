import { existsSync, readdirSync, readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { LOGO_KEYS } from "./harnessLogo";
import { HARNESS_LOGO_MANIFEST } from "./harnessLogoManifest";
import { hasMenuIcon, MENU_ICON_KEYS, MENU_ICONS_APP_VERSION } from "./harnessMenuIcons";

const sets = new URL("../assets/icons/harness/", import.meta.url);

type ImageSet = {
	images: { filename: string; scale: string; appearances?: { appearance: string; value: string }[] }[];
	properties?: { "template-rendering-intent"?: string };
};

// SwiftUI menus draw each harness mark by asset name, so a mark with no image
// set draws nothing there. scripts/sync-harness-logos.mjs writes them.
describe("harness menu image sets", () => {
	it("exist for exactly the harnesses that ship a mark", () => {
		const onDisk = readdirSync(sets).filter((name) => name.endsWith(".imageset")).sort();
		expect(onDisk).toEqual([...LOGO_KEYS].map((key) => `HarnessLogo-${key}.imageset`).sort());
	});

	it.each([...LOGO_KEYS])("ships %s at every scale, in its own colours", (key) => {
		const set = new URL(`HarnessLogo-${key}.imageset/`, sets);
		const contents = JSON.parse(readFileSync(new URL("Contents.json", set), "utf8")) as ImageSet;
		const dark = (image: ImageSet["images"][number]) =>
			image.appearances?.some((a) => a.appearance === "luminosity" && a.value === "dark") ?? false;
		expect(contents.images.filter((image) => !dark(image)).map((image) => image.scale)).toEqual(["1x", "2x", "3x"]);
		// A brand with a separate dark mark ships it as the set's dark appearance,
		// which iOS draws when the app is dark.
		const darkScales = HARNESS_LOGO_MANIFEST[key].dark ? ["1x", "2x", "3x"] : [];
		expect(contents.images.filter(dark).map((image) => image.scale)).toEqual(darkScales);
		for (const image of contents.images) expect(existsSync(new URL(image.filename, set)), image.filename).toBe(true);
		// A menu tints a template image like a symbol; a brand mark keeps its colours.
		expect(contents.properties?.["template-rendering-intent"]).toBe("original");
	});

	it("copies them into the app's asset catalog on prebuild", () => {
		const app = JSON.parse(readFileSync(new URL("../app.json", import.meta.url), "utf8")) as { expo: { plugins: unknown[] } };
		expect(app.expo.plugins).toContain("./plugins/withHarnessMenuIcons");
	});
});

// Image sets reach users only with a store build, but menu JS also ships as an
// OTA, so the menus draw by asset name only the marks installed binaries have.
describe("menu marks the installed binaries have", () => {
	const appVersion = (JSON.parse(readFileSync(new URL("../app.json", import.meta.url), "utf8")) as { expo: { version: string } }).expo.version;

	it("are each a harness with a mark and a shipped image set", () => {
		for (const key of MENU_ICON_KEYS) {
			expect(LOGO_KEYS.has(key), key).toBe(true);
			expect(existsSync(new URL(`HarnessLogo-${key}.imageset/Contents.json`, sets)), key).toBe(true);
		}
	});

	// The store build that bumps the version ships every image set in the tree,
	// so from then on every harness with a mark can be drawn by name.
	it("are updated with the store build that bumps the app version", () => {
		expect(
			appVersion,
			`app.json is now ${appVersion}: set MENU_ICON_KEYS in lib/harnessMenuIcons.ts to every key in LOGO_KEYS and MENU_ICONS_APP_VERSION to "${appVersion}"`,
		).toBe(MENU_ICONS_APP_VERSION);
	});

	it("draw a harness without one as the terminal symbol, not a missing asset", () => {
		expect(hasMenuIcon("codex")).toBe(true);
		expect(hasMenuIcon(" Codex ")).toBe(true);
		for (const key of [...LOGO_KEYS].filter((key) => !MENU_ICON_KEYS.has(key))) expect(hasMenuIcon(key), key).toBe(false);
		expect(hasMenuIcon("gemini")).toBe(false);
	});

	// Every catalog lookup goes through HarnessImage, which checks hasMenuIcon
	// first, so no other surface can ask for an image set a binary lacks.
	it("are looked up by asset name only in HarnessImage", () => {
		const lib = new URL("./", import.meta.url);
		const users = readdirSync(lib, { recursive: true, encoding: "utf8" })
			.filter((file) => /\.tsx?$/.test(file) && !/\.test\.tsx?$/.test(file))
			.filter((file) => /assetName=\{`HarnessLogo-/.test(readFileSync(new URL(file, lib), "utf8")));
		expect(users).toEqual(["spawn-composer-controls.ios.tsx"]);
		const source = readFileSync(new URL("spawn-composer-controls.ios.tsx", lib), "utf8");
		expect(source).toContain('if (!hasMenuIcon(harness)) return <Image systemName="terminal"');
	});
});
