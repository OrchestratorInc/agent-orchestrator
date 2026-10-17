import { describe, expect, it } from "vitest";
import { colorDistance, contrastRatio } from "../../shared/theme/color";
import { DEFAULT_THEME_STYLE, THEMES, type ThemeStyle } from "../../shared/theme/palettes";
import { darkTheme, lightTheme, terminalTheme, themeFor, type ColorScheme } from "./theme";
import { ensureContrast, FAINT_CONTRAST, HIGHLIGHT_STATE_DISTANCE, TEXT_CONTRAST } from "./themeDerive";

const SCHEMES: ColorScheme[] = ["dark", "light"];
const NAMED = THEMES.filter((theme) => theme.id !== DEFAULT_THEME_STYLE).map((theme) => theme.id as ThemeStyle);
const CASES = NAMED.flatMap((style) => SCHEMES.map((scheme) => [style, scheme] as const));

describe("ensureContrast", () => {
	it("returns a passing colour unchanged", () => {
		expect(ensureContrast("#000000", "#ffffff", 4.5, "#000000")).toBe("#000000");
	});

	it("moves a failing colour until it passes", () => {
		const fixed = ensureContrast("#ffdd55", "#ffffff", 4.5, "#000000");
		expect(contrastRatio(fixed, "#ffffff")).toBeGreaterThanOrEqual(4.5);
	});
});

describe("themeFor with a named theme", () => {
	it("keeps Orchestrate on the hand-tuned palettes", () => {
		expect(themeFor("dark", DEFAULT_THEME_STYLE)).toBe(darkTheme);
		expect(themeFor("light", DEFAULT_THEME_STYLE)).toBe(lightTheme);
		expect(themeFor("dark")).toBe(darkTheme);
	});

	it("keeps Orchestrate's highlight hue-less, so the default look is unchanged", () => {
		expect(darkTheme.highlight).toBe(darkTheme.accent);
		expect(lightTheme.highlight).toBe(lightTheme.accent);
	});

	it("returns one object per theme and scheme, so themed styles stay memoised", () => {
		expect(themeFor("dark", "catppuccin")).toBe(themeFor("dark", "catppuccin"));
		expect(themeFor("dark", "catppuccin")).not.toBe(themeFor("light", "catppuccin"));
	});

	it.each(CASES)("%s %s fills every slot", (style, scheme) => {
		const theme = themeFor(scheme, style);
		expect(Object.keys(theme).sort()).toEqual(Object.keys(darkTheme).sort());
		for (const [token, value] of Object.entries(theme)) expect(value, token).toMatch(/\S/);
	});

	it.each(CASES)("%s %s keeps text and state labels readable on the base", (style, scheme) => {
		const t = themeFor(scheme, style);
		const readable = {
			textPrimary: t.textPrimary,
			textSecondary: t.textSecondary,
			textTertiary: t.textTertiary,
			highlight: t.highlight,
			orange: t.orange,
			blue: t.blue,
			amber: t.amber,
			red: t.red,
			green: t.green,
			purple: t.purple,
		};
		for (const [token, color] of Object.entries(readable)) {
			expect(contrastRatio(color, t.bgBase), token).toBeGreaterThanOrEqual(TEXT_CONTRAST);
		}
		expect(contrastRatio(t.textFaint, t.bgBase)).toBeGreaterThanOrEqual(FAINT_CONTRAST);
	});

	it.each(CASES)("%s %s keeps primary actions hue-less", (style, scheme) => {
		const t = themeFor(scheme, style);
		expect(t.accent).toBe(t.textPrimary);
		expect(contrastRatio(t.onAccent, t.accent)).toBeGreaterThanOrEqual(TEXT_CONTRAST);
	});

	it.each(CASES)("%s %s keeps the highlight from passing for an attention state", (style, scheme) => {
		const t = themeFor(scheme, style);
		if (t.highlight === t.textPrimary) return;
		for (const [state, color] of Object.entries({ orange: t.orange, amber: t.amber, red: t.red, green: t.green })) {
			expect(colorDistance(t.highlight, color), state).toBeGreaterThanOrEqual(HIGHLIGHT_STATE_DISTANCE);
		}
	});

	it("falls back to the text colour where the accent is a state hue", () => {
		for (const style of ["monokai", "ayu", "everforest", "vitesse", "flexoki"] as const) {
			for (const scheme of SCHEMES) expect(themeFor(scheme, style).highlight, `${style} ${scheme}`).toBe(themeFor(scheme, style).textPrimary);
		}
	});

	it("keeps a blue or purple accent as the highlight", () => {
		for (const style of ["github", "catppuccin", "tokyo-night", "dracula"] as const) {
			for (const scheme of SCHEMES) expect(themeFor(scheme, style).highlight, `${style} ${scheme}`).not.toBe(themeFor(scheme, style).textPrimary);
		}
	});

	it("keeps Orchestrate's Android selection on the hue-less accent border", () => {
		expect(darkTheme.selection).toBe(darkTheme.accentBorder);
		expect(lightTheme.selection).toBe(lightTheme.accentBorder);
	});

	it.each(NAMED)("%s light lifts cards off the base", (style) => {
		const t = themeFor("light", style);
		expect(contrastRatio(t.bgElevated, t.bgBase)).toBeGreaterThanOrEqual(1.05);
	});
});

describe("terminalTheme with a named theme", () => {
	it.each(CASES)("%s %s defines every slot and collapses black into the background", (style, scheme) => {
		const terminal = terminalTheme(scheme, style);
		expect(Object.keys(terminal).sort()).toEqual(Object.keys(terminalTheme(scheme)).sort());
		expect(terminal.black).toBe(terminal.background);
	});

	it.each(CASES)("%s %s keeps the cursor visible on the terminal background", (style, scheme) => {
		const terminal = terminalTheme(scheme, style);
		expect(contrastRatio(terminal.cursor, terminal.background)).toBeGreaterThanOrEqual(FAINT_CONTRAST);
	});
});
