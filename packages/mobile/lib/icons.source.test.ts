import { existsSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// Lucide renames glyphs between versions and keeps the old name in its type
// declarations only, so tsc passes and Metro fails on launch. Check every
// imported glyph against the runtime build the bundler actually resolves.
describe("icon imports", () => {
	it("resolves every Lucide glyph to a shipped module", () => {
		const source = readFileSync(fileURLToPath(new URL("./icons.tsx", import.meta.url)), "utf8");
		const names = [...source.matchAll(/from "lucide-react-native\/icons\/([a-z0-9-]+)"/g)].map((match) => match[1]);
		expect(names.length).toBeGreaterThan(50);
		const missing = names.filter((name) => !existsSync(fileURLToPath(new URL(`../node_modules/lucide-react-native/dist/cjs/icons/${name}.js`, import.meta.url))));
		expect(missing).toEqual([]);
	});
});

// The icon's `name` takes any string, and an unknown one draws nothing: the
// tag page shipped blank checkboxes ("square", "check-square") and a blank tag
// glyph that the typecheck let through. Every literal name must be shipped.
describe("icon names", () => {
	it("draws only glyphs the app ships", async () => {
		const { readdirSync } = await import("node:fs");
		const icons = readFileSync(fileURLToPath(new URL("./icons.tsx", import.meta.url)), "utf8");
		const block = icons.slice(icons.indexOf("export const glyphs = {"), icons.indexOf("} as const;"));
		const glyphs = new Set([...block.matchAll(/^\t"([a-z0-9-]+)":/gm)].map((match) => match[1]));
		const root = fileURLToPath(new URL("../", import.meta.url));
		const files = ["app", "lib"].flatMap((dir) => readdirSync(`${root}${dir}`, { recursive: true, encoding: "utf8" })
			.filter((file) => file.endsWith(".tsx") && !file.includes(".test."))
			.map((file) => `${root}${dir}/${file}`));
		const missing = files.flatMap((file) => [...readFileSync(file, "utf8").matchAll(/<Feather name="([a-z0-9-]+)"/g)]
			.map((match) => match[1])
			.filter((name) => !glyphs.has(name))
			.map((name) => `${file.slice(root.length)}: ${name}`));
		expect(missing).toEqual([]);
	});
});
