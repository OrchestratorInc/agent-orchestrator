import { existsSync, readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
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

	// `name` also takes a plain string (for names carried in data), so a glyph
	// dropped from the map still typechecks and the icon silently draws nothing:
	// #7839 pruned chevrons, `info`, `x-circle` and `user` that screens still used.
	it("has a glyph for every icon name the app writes literally", () => {
		const root = fileURLToPath(new URL("..", import.meta.url));
		const glyphs = new Set([...readFileSync(join(root, "lib/icons.tsx"), "utf8").matchAll(/^\t"([a-z0-9-]+)":/gm)].map((match) => match[1]));
		const files = ["app", "lib"].flatMap((dir) => readdirSync(join(root, dir), { recursive: true, encoding: "utf8" }).map((file) => join(dir, file)))
			.filter((file) => file.endsWith(".tsx") && !file.includes(".test."));
		const unknown: string[] = [];
		for (const file of files) {
			for (const [, literal, expression] of readFileSync(join(root, file), "utf8").matchAll(/<Feather\b[^>]*?\bname=(?:"([^"]+)"|\{([^}]*)\})/g)) {
				// In an expression, the names are the ternary's arms, not what the condition compares.
				const names = literal ? [literal] : [...expression!.matchAll(/[?:]\s*"([^"]+)"/g)].map((match) => match[1]);
				for (const name of names) if (!glyphs.has(name!)) unknown.push(`${file}: ${name}`);
			}
		}
		expect(glyphs.size).toBeGreaterThan(50);
		expect(files.length).toBeGreaterThan(100);
		expect(unknown).toEqual([]);
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
