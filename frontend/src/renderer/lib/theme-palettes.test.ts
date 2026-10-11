import { describe, expect, it } from "vitest";
import { contrastRatio, lightness } from "./theme-color";
import { THEMES, type ThemePalette } from "./theme-palettes";

const HUE_ROLES = ["accent", "red", "orange", "yellow", "green", "blue", "purple", "cyan"] as const;

// Orchestrate's palette is a swatch mirror of tokens.css, which owns its values.
const cases = THEMES.filter((theme) => theme.id !== "orchestrate").flatMap((theme) =>
	(["dark", "light"] as const).map((mode) => ({ id: theme.id, mode, palette: theme[mode] as ThemePalette })),
);

function ratios(palette: ThemePalette, roles: readonly (keyof ThemePalette)[], surface: string) {
	return Object.fromEntries(roles.map((role) => [role, Number(contrastRatio(palette[role]!, surface).toFixed(2))]));
}

describe("theme palettes", () => {
	it.each(cases)("$id $mode palette matches its mode", ({ mode, palette }) => {
		expect(lightness(palette.bg) < 0.5).toBe(mode === "dark");
	});

	it.each(cases)("$id $mode text clears AAA on every surface", ({ palette }) => {
		const surfaces = { bg: palette.bg, panel: palette.panel, sidebar: palette.sidebar };
		const failing = Object.entries(surfaces).filter(([, surface]) => contrastRatio(palette.text, surface) < 7);
		expect(failing).toEqual([]);
	});

	it.each(cases)("$id $mode muted text clears AA on the canvas", ({ palette }) => {
		expect(contrastRatio(palette.muted, palette.bg)).toBeGreaterThanOrEqual(4.5);
	});

	it.each(cases)("$id $mode accent and status hues clear 3:1 on the canvas", ({ palette }) => {
		const failing = Object.entries(ratios(palette, HUE_ROLES, palette.bg)).filter(([, ratio]) => ratio < 3);
		expect(failing).toEqual([]);
	});
});
