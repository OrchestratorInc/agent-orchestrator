import { describe, expect, it } from "vitest";
import { THEMES } from "../../../packages/shared/theme/palettes";
import { lowContrastRoles, parseCustomTheme } from "./custom-themes";

const dracula = THEMES.find((theme) => theme.id === "dracula")!;
const file = (overrides: Record<string, unknown> = {}) => JSON.stringify({ name: "Sunset", dark: dracula.dark, light: dracula.light, ...overrides });

describe("parseCustomTheme", () => {
	it("takes its id from the file name and its label from the file", () => {
		expect(parseCustomTheme("Sun_set-2.JSON", file())).toEqual({
			id: "custom:sun_set-2",
			label: "Sunset",
			file: "Sun_set-2.JSON",
			dark: dracula.dark,
			light: dracula.light,
			lowContrast: [],
		});
	});

	it("keeps an optional primary and drops keys it does not know", () => {
		const parsed = parseCustomTheme("a.json", file({ dark: { ...dracula.dark, primary: "#fff", tokens: { "--border": "red" } }, css: "body{}" }));
		expect(parsed).toMatchObject({ dark: { ...dracula.dark, primary: "#fff" } });
		expect(JSON.stringify(parsed)).not.toContain("--border");
	});

	it.each([
		["My Theme.json", file(), { code: "fileName" }],
		['x"]{}.json', file(), { code: "fileName" }],
		["a.json", "{ name: ", { code: "json" }],
		["a.json", "[]", { code: "json" }],
		["a.json", file({ name: "  " }), { code: "name" }],
		["a.json", file({ name: "x".repeat(61) }), { code: "name" }],
		["a.json", file({ light: undefined }), { code: "color", mode: "light", role: "bg" }],
		["a.json", file({ dark: { ...dracula.dark, cyan: undefined } }), { code: "color", mode: "dark", role: "cyan" }],
		["a.json", file({ dark: { ...dracula.dark, primary: "white" } }), { code: "color", mode: "dark", role: "primary" }],
	])("rejects %s %#", (name, text, problem) => {
		expect(parseCustomTheme(name, text)).toEqual({ file: name, problem });
	});

	// Colors are written into a <style> element.
	it.each(["red;} body{display:none", "#12345", "#gggggg", "rgb(0 0 0)", "#11223344", 0])("rejects the color %j", (color) => {
		expect(parseCustomTheme("a.json", file({ dark: { ...dracula.dark, accent: color } }))).toEqual({
			file: "a.json",
			problem: { code: "color", mode: "dark", role: "accent" },
		});
	});

	it("loads a low-contrast theme and names the roles that are hard to read", () => {
		const parsed = parseCustomTheme("faint.json", file({ dark: { ...dracula.dark, muted: "#3a3c4a", red: "#30323e" } }));
		expect(parsed).toMatchObject({ id: "custom:faint", lowContrast: ["dark.muted", "dark.red"] });
	});
});

describe("lowContrastRoles", () => {
	it("passes every built-in palette, so a copied built-in never warns", () => {
		const failing = THEMES.filter((theme) => theme.id !== "orchestrate").flatMap((theme) =>
			(["dark", "light"] as const).flatMap((mode) => lowContrastRoles(theme[mode]).map((role) => `${theme.id}.${mode}.${role}`)),
		);
		expect(failing).toEqual([]);
	});

	it("flags text that is readable on the canvas but not on the sidebar", () => {
		expect(lowContrastRoles({ ...dracula.dark, sidebar: "#9a9a9a" })).toEqual(["text"]);
	});
});
