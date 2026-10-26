import { readdirSync, readFileSync, statSync } from "node:fs";
import { createRequire } from "node:module";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it, vi } from "vitest";

vi.mock("@expo/ui/swift-ui/modifiers", () => ({ font: (params: object) => ({ font: params }) }));

import { geistFont } from "./swiftUIFont";

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

describe("geistFont", () => {
	it("sets a ramp step in Geist at the ramp's size, scaled by its text style", () => {
		expect(geistFont("body")).toEqual({ font: { family: "Geist-Regular", size: 17, textStyle: "body" } });
		expect(geistFont("title1")).toEqual({ font: { family: "Geist-SemiBold", size: 28, textStyle: "title" } });
		expect(geistFont("footnote", { weight: "500" })).toEqual({ font: { family: "Geist-Medium", size: 13, textStyle: "footnote" } });
		expect(geistFont("footnote", { mono: true })).toEqual({ font: { family: "GeistMono-Regular", size: 13, textStyle: "footnote" } });
	});

	// Font.custom finds a face by the PostScript name inside its file, not by
	// expo-font's alias, so each name has to be the one the bundled file carries.
	it.each([
		["@expo-google-fonts/geist/400Regular/Geist_400Regular.ttf", familyOf(geistFont("body", { weight: "400" }))],
		["@expo-google-fonts/geist/500Medium/Geist_500Medium.ttf", familyOf(geistFont("body", { weight: "500" }))],
		["@expo-google-fonts/geist/600SemiBold/Geist_600SemiBold.ttf", familyOf(geistFont("body", { weight: "600" }))],
		["@expo-google-fonts/geist-mono/400Regular/GeistMono_400Regular.ttf", familyOf(geistFont("body", { mono: true }))],
	])("names %s by its PostScript name", (file, family) => {
		const name = Buffer.from(family, "utf16le").swap16();
		expect(readFileSync(require.resolve(file)).includes(name)).toBe(true);
	});

	it("is the only place SwiftUI text names a font family", () => {
		const direct = [...sources("app"), ...sources("lib")]
			.filter((path) => path !== join("lib", "swiftUIFont.ts"))
			.filter((path) => /\bfont\(\{[^}]*\bfamily:/.test(readFileSync(join(root, path), "utf8")));
		expect(direct).toEqual([]);
	});
});
