import { describe, expect, it } from "vitest";
import { THEME_COLOR_ROLES, builtInThemeFile, isThemeColorRole, serializeThemeFile } from "./theme-file";
import { getThemeDefinition } from "./theme-palettes";

describe("theme color roles", () => {
	it("has no duplicates and knows the surface, chat, sidebar, terminal and hue roles", () => {
		expect(new Set(THEME_COLOR_ROLES).size).toBe(THEME_COLOR_ROLES.length);
		for (const role of ["canvas", "sidebarRowSelected", "messageSurface", "terminalCursor", "link", "cyan"]) {
			expect(isThemeColorRole(role)).toBe(true);
		}
		expect(isThemeColorRole("sidebarBg")).toBe(false);
	});
});

describe("serializeThemeFile", () => {
	it("writes the file a person shares: version first, no save time, no empty parts", () => {
		const text = serializeThemeFile({
			id: "sumi",
			name: "Sumi",
			appearance: "dark",
			colors: { canvas: "#101012" },
			variants: {},
			css: "",
		});
		expect(JSON.parse(text)).toEqual({ version: 1, id: "sumi", name: "Sumi", appearance: "dark", colors: { canvas: "#101012" } });
		expect(text.endsWith("}\n")).toBe(true);
		expect(text.indexOf('"version"')).toBeLessThan(text.indexOf('"id"'));
	});

	it("keeps only the other appearance's variant, and the CSS", () => {
		const file = JSON.parse(
			serializeThemeFile({
				id: "paper",
				name: "Paper",
				appearance: "light",
				colors: { canvas: "#fbf7ef" },
				variants: { dark: { canvas: "#101012" }, light: { canvas: "#ffffff" } },
				css: ":root { --radius: 0; }",
			}),
		);
		expect(file.variants).toEqual({ dark: { canvas: "#101012" } });
		expect(file.css).toBe(":root { --radius: 0; }");
	});
});

describe("builtInThemeFile", () => {
	it("turns a built-in theme into a starting point with both appearances", () => {
		const nord = getThemeDefinition("nord");
		const file = builtInThemeFile(nord);
		expect(file.id).toBe("nord-copy");
		expect(file.appearance).toBe("dark");
		expect(file.colors).toMatchObject({ canvas: nord.dark.bg, sidebar: nord.dark.sidebar, text: nord.dark.text, link: nord.dark.accent });
		expect(file.variants?.light).toMatchObject({ canvas: nord.light.bg, text: nord.light.text });
		for (const role of Object.keys(file.colors ?? {})) expect(isThemeColorRole(role)).toBe(true);
	});
});
