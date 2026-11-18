import { existsSync, readdirSync, readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { LOGO_KEYS } from "./harnessLogo";
import { HARNESS_LOGO_MANIFEST } from "./harnessLogoManifest";

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
