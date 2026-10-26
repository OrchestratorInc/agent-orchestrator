import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { symbols } from "./symbols";
import { toolbarIcon, type ToolbarSymbolName } from "./toolbarIcon";
import { materialDrawableNames } from "./toolbarIcon.types";

const source = (path: string) => readFileSync(fileURLToPath(new URL(path, import.meta.url)), "utf8");
const drawable = (name: string) => fileURLToPath(new URL(`../assets/icons/${name}.xml`, import.meta.url));
const root = fileURLToPath(new URL("..", import.meta.url));

function sources(dir: string): string[] {
	return readdirSync(join(root, dir)).flatMap((name) => {
		const path = join(dir, name);
		if (statSync(join(root, path)).isDirectory()) return sources(path);
		return /\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name) ? [path] : [];
	});
}

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

	// One registry: every Android surface asks `materialDrawable`, so each
	// drawable is required once, and every file in assets/icons is registered.
	it("is the only module that bundles a drawable", () => {
		const elsewhere = [...sources("app"), ...sources("lib")]
			.filter((path) => path !== join("lib", "toolbarIcon.android.ts"))
			.filter((path) => /require\([^)]*\.xml["']\)/.test(readFileSync(join(root, path), "utf8")));
		expect(elsewhere).toEqual([]);
	});

	it("bundles every drawable in assets/icons", () => {
		const files = readdirSync(join(root, "assets", "icons")).filter((name) => name.endsWith(".xml")).map((name) => name.slice(0, -".xml".length));
		expect(files.sort()).toEqual([...materialDrawableNames].sort());
	});
});
