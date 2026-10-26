import { existsSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { chatMenuIcon, type ChatMenuIcon } from "./chatMenuIcons";

const names: ChatMenuIcon[] = ["edit", "copy", "photo", "file", "model", "effort", "fast", "permissions", "setting"];

describe("chat menu icons", () => {
	it("names an SF Symbol for every action on iOS", () => {
		for (const name of names) expect(typeof chatMenuIcon(name), name).toBe("string");
		expect(chatMenuIcon("copy")).toBe("doc.on.doc");
	});

	// Android's map is bundled by `require`, which vitest cannot load, so its
	// sources are checked on disk: a missing drawable fails the Android bundle.
	it("bundles a Material Symbol drawable for every action on Android", () => {
		const android = readFileSync(fileURLToPath(new URL("./chatMenuIcons.android.ts", import.meta.url)), "utf8");
		const required = [...android.matchAll(/(\w+): require\("\.\.\/\.\.\/assets\/icons\/(\w+)\.xml"\)/g)];
		expect(required.map(([, name]) => name).sort()).toEqual([...names].sort());
		for (const [, , file] of required) {
			expect(existsSync(fileURLToPath(new URL(`../../assets/icons/${file}.xml`, import.meta.url))), file).toBe(true);
		}
	});
});
