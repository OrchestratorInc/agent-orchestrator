import { existsSync, readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { LOGO_KEYS } from "./harnessLogo";

const sets = new URL("../assets/icons/harness/", import.meta.url);

// SwiftUI menus draw each harness mark by asset name, so a mark with no image
// set draws nothing there. scripts/generate-harness-menu-icons.mjs writes them.
describe("harness menu image sets", () => {
	it.each([...LOGO_KEYS])("ships %s at every scale, in its own colours", (key) => {
		const set = new URL(`HarnessLogo-${key}.imageset/`, sets);
		const contents = JSON.parse(readFileSync(new URL("Contents.json", set), "utf8")) as {
			images: { filename: string; scale: string }[];
			properties?: { "template-rendering-intent"?: string };
		};
		expect(contents.images.map((image) => image.scale)).toEqual(["1x", "2x", "3x"]);
		for (const image of contents.images) expect(existsSync(new URL(image.filename, set)), image.filename).toBe(true);
		// A menu tints a template image like a symbol; a brand mark keeps its colours.
		expect(contents.properties?.["template-rendering-intent"]).toBe("original");
	});

	it("copies them into the app's asset catalog on prebuild", () => {
		const app = JSON.parse(readFileSync(new URL("../app.json", import.meta.url), "utf8")) as { expo: { plugins: unknown[] } };
		expect(app.expo.plugins).toContain("./plugins/withHarnessMenuIcons");
	});
});
