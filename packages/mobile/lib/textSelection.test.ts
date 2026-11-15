import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it, vi } from "vitest";
import { darkTheme, lightTheme, themeFor } from "./theme";

let platformOS = "android";

vi.mock("react-native", () => ({
	Platform: {
		get OS() {
			return platformOS;
		},
	},
}));

const { textSelectionColors } = await import("./textSelection");

function alpha(color: string): number {
	const match = /^rgba\([^)]*,\s*([\d.]+)\)$/.exec(color);
	return match ? Number(match[1]) : 1;
}

describe("textSelectionColors", () => {
	it.each([["dark", darkTheme], ["light", lightTheme]] as const)("keeps selected text readable on Android (%s)", (_name, t) => {
		platformOS = "android";
		const colors = textSelectionColors(t);
		// An opaque highlight in the text's own color hides the selected characters.
		expect(alpha(String(colors.selectionColor))).toBeLessThan(0.5);
		expect(colors.cursorColor).toBe(t.highlight);
		expect(colors.selectionHandleColor).toBe(t.highlight);
	});

	it("uses a named theme's selection fill and highlight", () => {
		const t = themeFor("dark", "github");
		expect(t.highlight).not.toBe(t.accent);
		platformOS = "android";
		expect(textSelectionColors(t)).toEqual({ selectionColor: t.selection, cursorColor: t.highlight, selectionHandleColor: t.highlight });
		platformOS = "ios";
		expect(textSelectionColors(t)).toEqual({ selectionColor: t.highlight });
	});

	it("keeps the solid highlight on iOS, which tints the selection itself", () => {
		platformOS = "ios";
		expect(textSelectionColors(darkTheme)).toEqual({ selectionColor: darkTheme.highlight });
	});
});

function sourceFiles(dir: string): string[] {
	return readdirSync(dir).flatMap((name) => {
		const path = join(dir, name);
		if (statSync(path).isDirectory()) return sourceFiles(path);
		return /\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name) ? [path] : [];
	});
}

it("routes every React Native TextInput through the shared selection colors", () => {
	const root = fileURLToPath(new URL("..", import.meta.url));
	const files = ["app", "lib"].flatMap((dir) => sourceFiles(join(root, dir)));
	const offenders: string[] = [];
	for (const file of files) {
		const source = readFileSync(file, "utf8");
		// A theme token passed straight through skips the Android translucency.
		if (/(selectionColor|cursorColor|selectionHandleColor)=\{t\./.test(source)) offenders.push(`${file}: theme color set directly`);
		for (const input of source.match(/<TextInput\s[\s\S]*?\/>/g) ?? []) {
			if (!input.includes("textSelectionColors(")) offenders.push(`${file}: TextInput without textSelectionColors`);
		}
	}
	expect(offenders).toEqual([]);
});
