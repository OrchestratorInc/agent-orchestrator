import { readdirSync, readFileSync, statSync } from "node:fs";
import { createRequire } from "node:module";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it, vi } from "vitest";

vi.mock("@expo/ui/swift-ui/modifiers", () => ({ font: (params: object) => ({ font: params }) }));

import { appFont } from "./swiftUIFont";

const root = fileURLToPath(new URL("..", import.meta.url));
const require = createRequire(import.meta.url);

function sources(dir: string): string[] {
	return readdirSync(join(root, dir)).flatMap((name) => {
		const path = join(dir, name);
		if (statSync(join(root, path)).isDirectory()) return sources(path);
		return /\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name) ? [path] : [];
	});
}

const familyOf = (modifier: unknown) => (modifier as { font: { family: string } }).font.family;

describe("appFont", () => {
	// Font.custom finds a face by the PostScript name inside its file, not by
	// expo-font's alias, so each name has to be the one the bundled file carries.
	it.each([
		["assets/fonts/dm-sans/DMSans_400Regular.ttf", familyOf(appFont("body", { weight: "400" }))],
		["assets/fonts/dm-sans/DMSans_500Medium.ttf", familyOf(appFont("body", { weight: "500" }))],
		["assets/fonts/dm-sans/DMSans_600SemiBold.ttf", familyOf(appFont("body", { weight: "600" }))],
		["@expo-google-fonts/geist-mono/400Regular/GeistMono_400Regular.ttf", familyOf(appFont("body", { mono: true }))],
	])("names %s by its PostScript name", (file, family) => {
		const name = Buffer.from(family, "utf16le").swap16();
		const path = file.startsWith("@") ? require.resolve(file) : join(root, file);
		expect(readFileSync(path).includes(name)).toBe(true);
	});

	// A point size alone ignores Dynamic Type, so every SwiftUI font scales from
	// a text style.
	it("sizes every SwiftUI font from a text style", () => {
		const fixed = [...sources("app"), ...sources("lib")]
			.filter((path) => (readFileSync(join(root, path), "utf8").match(/\bfont\(\{[^}]*\}/g) ?? []).some((call) => !call.includes("textStyle")));
		expect(fixed).toEqual([]);
	});
});
