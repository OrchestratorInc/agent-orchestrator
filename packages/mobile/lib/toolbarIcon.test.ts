import { existsSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { symbols } from "./symbols";
import { toolbarIcon, type ToolbarSymbolName } from "./toolbarIcon";
import { materialDrawableNames } from "./toolbarIcon.types";

const source = (path: string) => readFileSync(fileURLToPath(new URL(path, import.meta.url)), "utf8");
const drawable = (name: string) => fileURLToPath(new URL(`../assets/icons/${name}.xml`, import.meta.url));

describe("toolbarIcon", () => {
	it("names the SF Symbol on iOS, which UIKit draws in the bar's tint", () => {
		expect(toolbarIcon("openExternal")).toBe(symbols.openExternal.ios);
		expect(toolbarIcon({ ios: "ellipsis", android: "more_vert" })).toBe("ellipsis");
	});

	// Typecheck holds this list to registry names with a bundled drawable.
	it("covers the header glyphs the app declares", () => {
		const names: ToolbarSymbolName[] = ["add", "back", "checkmark", "close", "delete", "devices", "filter", "folder", "more", "notifications", "openExternal", "projects", "settings", "wrapLines"];
		const bundled: readonly string[] = materialDrawableNames;
		expect(names.filter((name) => !bundled.includes(symbols[name].android))).toEqual([]);
	});

	// Android's header items draw only an image source, and a missing drawable
	// is a bundle-time failure, so every listed name needs its file and its
	// require, and each file has to be a Material Symbol outline.
	it.each(materialDrawableNames)("bundles %s as a Material Symbol drawable", (name) => {
		expect(existsSync(drawable(name))).toBe(true);
		const xml = readFileSync(drawable(name), "utf8");
		expect(xml).toContain('android:viewportWidth="960"');
		expect(xml).toMatch(/android:pathData="M[^"]+Z"/);
		expect(source("./toolbarIcon.android.ts")).toContain(`${name}: require("../assets/icons/${name}.xml")`);
	});
});
