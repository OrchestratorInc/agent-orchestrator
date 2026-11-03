import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { symbols } from "./symbols";
import { toolbarIcon, type ToolbarSymbolName } from "./toolbarIcon";
import { materialDrawableNames } from "./toolbarIcon.types";

const source = (path: string) => readFileSync(fileURLToPath(new URL(path, import.meta.url)), "utf8");
const root = fileURLToPath(new URL("..", import.meta.url));

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
	// require, and every file in assets/icons is registered.
	it("bundles every listed drawable, and lists every file in assets/icons", () => {
		const registry = source("./toolbarIcon.android.ts");
		for (const name of materialDrawableNames) expect(registry).toContain(`${name}: require("../assets/icons/${name}.xml")`);
		const files = readdirSync(join(root, "assets", "icons")).filter((name) => name.endsWith(".xml")).map((name) => name.slice(0, -".xml".length));
		expect(files.sort()).toEqual([...materialDrawableNames].sort());
	});
});
