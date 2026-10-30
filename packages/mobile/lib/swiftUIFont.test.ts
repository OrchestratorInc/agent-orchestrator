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
	it("sets a ramp step in DM Sans at the ramp's size, scaled by its text style", () => {
		expect(appFont("body")).toEqual({ font: { family: "DMSans-Regular", size: 16, textStyle: "body" } });
		expect(appFont("title1")).toEqual({ font: { family: "DMSans-SemiBold", size: 26, textStyle: "title" } });
		expect(appFont("footnote", { weight: "500" })).toEqual({ font: { family: "DMSans-Medium", size: 13, textStyle: "footnote" } });
		expect(appFont("footnote", { mono: true })).toEqual({ font: { family: "GeistMono-Regular", size: 13, textStyle: "footnote" } });
	});

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

	it("is the only place SwiftUI text names a font family", () => {
		const direct = [...sources("app"), ...sources("lib")]
			.filter((path) => path !== join("lib", "swiftUIFont.ts"))
			.filter((path) => /\bfont\(\{[^}]*\bfamily:/.test(readFileSync(join(root, path), "utf8")));
		expect(direct).toEqual([]);
	});

	// A point size alone ignores Dynamic Type, so every SwiftUI font scales from
	// a text style.
	it("sizes every SwiftUI font from a text style", () => {
		const fixed = [...sources("app"), ...sources("lib")]
			.filter((path) => (readFileSync(join(root, path), "utf8").match(/\bfont\(\{[^}]*\}/g) ?? []).some((call) => !call.includes("textStyle")));
		expect(fixed).toEqual([]);
	});
});
