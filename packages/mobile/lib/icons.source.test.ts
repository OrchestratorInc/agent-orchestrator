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
