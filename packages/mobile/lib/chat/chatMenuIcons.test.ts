import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { materialDrawableNames } from "../toolbarIcon.types";
import { chatMenuIcon, type ChatMenuIcon } from "./chatMenuIcons";
import { chatMenuSymbols } from "./chatMenuIcons.types";

const names = Object.keys(chatMenuSymbols) as ChatMenuIcon[];

describe("chat menu icons", () => {
	it("names an SF Symbol for every action on iOS", () => {
		for (const name of names) expect(typeof chatMenuIcon(name), name).toBe("string");
		expect(chatMenuIcon("copy")).toBe("doc.on.doc");
	});

	// Android's menu draws an image source, which the shared registry bundles
	// (lib/toolbarIcon.test.ts checks its files); vitest cannot load the
	// `require`s, so the Android side is checked on disk.
	it("draws every action's Material Symbol from the shared drawable registry on Android", () => {
		for (const name of names) expect(materialDrawableNames, name).toContain(chatMenuSymbols[name].android);
		const android = readFileSync(fileURLToPath(new URL("./chatMenuIcons.android.ts", import.meta.url)), "utf8");
		expect(android).toContain("materialDrawable(chatMenuSymbols[name].android)");
		expect(android).not.toContain("require(");
	});
});
