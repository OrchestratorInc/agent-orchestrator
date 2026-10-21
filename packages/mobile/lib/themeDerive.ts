// Named desktop themes, translated into the phone's palette. Pure — no React
// Native imports — so the contrast rules are unit-testable.
//
// The catalog itself lives in packages/shared/theme/palettes.ts, which desktop
// reads too. Each palette is a handful of roles (canvas, panel, text, muted,
// accent, seven hues); this file maps those roles onto the slots in theme.ts,
// keeping the phone's own conventions rather than desktop's:
//
//  - Interactive emphasis stays hue-less. A filled button takes the theme's
//    text colour, never its accent: several themes (Catppuccin, Dracula) use
//    their purple as the accent, which is exactly the "merged" hue, and a button
//    must never read as a worker state. The accent goes to `highlight` instead:
//    links, text selection and the cursor, unless it matches a state that asks
//    for attention (see chooseHighlight).
//  - States keep the phone's meanings: orange is a working agent and amber
//    (the palette's yellow) is "needs you", whatever desktop does with them.
//  - Light surfaces follow the iOS grouped-list convention, a slightly darker
//    base under lighter cards. Several light palettes (Vercel, GitHub) put the
//    panel and the canvas within a shade of each other, which on a phone leaves
//    cards invisible.
//  - Text and state colours are pushed until they clear contrast on the base
//    they render on. The palettes are only checked against their own canvas,
//    and the darker grouped-list base can take a muted grey below the floor.
import { colorDistance, contrastRatio, mixColor, withAlpha } from "../../shared/theme/color";
import type { ThemePalette } from "../../shared/theme/palettes";
import type { ColorScheme, TerminalTheme, Theme } from "./theme";

/** Body text and state labels. */
export const TEXT_CONTRAST = 4.5;
/** The decorative step (`textFaint`), matching the hand-tuned palette. */
export const FAINT_CONTRAST = 3;
/**
 * How far (OKLab) the highlight must sit from every attention state. Themes
 * whose accent *is* one of those hues (Monokai's yellow, Everforest's green,
 * Flexoki's orange) land at or near 0, well inside this; distinct hues sit
 * further apart.
 */
export const HIGHLIGHT_STATE_DISTANCE = 0.08;

/**
 * The theme's accent, unless it could pass for a state that asks for attention:
 * working (orange), needs you (yellow), failing (red) or ready (green). A link
 * or cursor in "needs you" yellow reads as a status, so such themes fall back to
 * the hue-less text colour. Blue (exited) and purple (merged) are settled states
 * and the accents of most themes, so they stay allowed.
 *
 * Compared as rendered, after the contrast push: darkening for a light plate can
 * pull two distinct hues together (Rosé Pine Dawn's iris and love).
 */
export function chooseHighlight(accent: string, attentionStates: string[], text: string): string {
	return attentionStates.some((state) => colorDistance(accent, state) < HIGHLIGHT_STATE_DISTANCE) ? text : accent;
}

/**
 * `color`, moved toward `toward` in small steps until it clears `min` against
 * `bg`. Colours that already pass come back unchanged, so a theme's canonical
 * values survive wherever they work.
 */
export function ensureContrast(color: string, bg: string, min: number, toward: string): string {
	if (contrastRatio(color, bg) >= min) return color;
	for (let amount = 0.05; amount < 1; amount += 0.05) {
		const next = mixColor(color, toward, amount);
		if (contrastRatio(next, bg) >= min) return next;
	}
	return toward;
}

export function deriveMobileTheme(p: ThemePalette, scheme: ColorScheme, fontMono: string): Theme {
	const dark = scheme === "dark";
	// The extreme a colour moves toward when it needs more contrast: lighter on
	// a dark plate, darker on a light one. Pure white/black rather than the
	// theme's text colour, so a yellow on Tokyo Night Day doesn't drift blue.
	const extreme = dark ? "#ffffff" : "#000000";

	const bgElevated = dark ? p.panel : mixColor(p.panel, "#ffffff", 0.6);
	const bgBase = dark ? p.bg : mixColor(p.sidebar, p.text, 0.04);
	const readable = (color: string) => ensureContrast(color, bgBase, TEXT_CONTRAST, extreme);
	const tint = (color: string) => withAlpha(color, dark ? 0.14 : 0.12);

	const textSecondary = readable(p.muted);
	const orange = readable(p.orange);
	const blue = readable(p.blue);
	const amber = readable(p.yellow);
	const red = readable(p.red);
	const green = readable(p.green);
	const purple = readable(p.purple);
	const highlight = chooseHighlight(readable(p.accent), [orange, amber, red, green], p.text);

	return {
		bgBase,
		bgSide: dark ? p.sidebar : mixColor(p.sidebar, p.text, 0.07),
		bgColumn: dark ? mixColor(p.bg, p.panel, 0.4) : mixColor(bgBase, bgElevated, 0.5),
		bgSurface: dark ? mixColor(p.bg, p.panel, 0.6) : bgElevated,
		bgElevated,
		bgElevatedHover: dark ? mixColor(p.panel, p.element, 0.5) : p.element,
		bgSubtle: withAlpha(p.text, 0.04),

		textPrimary: p.text,
		textSecondary,
		textTertiary: readable(mixColor(p.muted, p.bg, 0.15)),
		textFaint: ensureContrast(mixColor(p.muted, p.bg, 0.35), bgBase, FAINT_CONTRAST, extreme),

		borderSubtle: withAlpha(p.text, 0.06),
		borderDefault: withAlpha(p.text, dark ? 0.1 : 0.12),
		borderStrong: withAlpha(p.text, dark ? 0.16 : 0.2),

		accent: p.text,
		accentTint: withAlpha(p.text, dark ? 0.14 : 0.08),
		accentBorder: withAlpha(p.text, dark ? 0.28 : 0.24),
		accentPressed: withAlpha(p.text, dark ? 0.22 : 0.16),
		highlight,
		selection: withAlpha(highlight, dark ? 0.28 : 0.24),

		orange,
		blue,
		amber,
		red,
		green,
		purple,

		tintOrange: tint(orange),
		tintBlue: tint(blue),
		tintAmber: tint(amber),
		tintRed: tint(red),
		tintGreen: tint(green),
		tintPurple: tint(purple),

		// Text clears AA on the canvas by construction, so the canvas clears AA on it.
		onAccent: p.bg,
		scrim: dark ? "rgba(0,0,0,0.6)" : "rgba(0,0,0,0.45)",

		fontMono,
	};
}

/**
 * The terminal's ANSI ramp for a named theme, built the way desktop's
 * theme-tokens.ts builds it so the same agent output looks the same on both.
 * Black collapses into the background, as in the hand-tuned ramp.
 */
export function deriveTerminalTheme(p: ThemePalette, scheme: ColorScheme): TerminalTheme {
	const bright = (color: string) => mixColor(color, scheme === "dark" ? "#ffffff" : p.text, 0.22);
	return {
		background: p.bg,
		foreground: p.text,
		// Desktop's named-theme cursor (terminal-themes.ts): the primary fill on a
		// dark plate, the foreground on a light one, where an orange block blinks
		// as a low-contrast wash.
		cursor: scheme === "dark" ? (p.primary ?? p.accent) : p.text,
		black: p.bg,
		red: p.red,
		green: p.green,
		yellow: p.yellow,
		blue: p.blue,
		magenta: p.purple,
		cyan: p.cyan,
		white: p.muted,
		brightBlack: mixColor(p.muted, p.bg, 0.25),
		brightRed: bright(p.red),
		brightGreen: bright(p.green),
		brightYellow: bright(p.yellow),
		brightBlue: bright(p.blue),
		brightMagenta: bright(p.purple),
		brightCyan: bright(p.cyan),
		brightWhite: p.text,
	};
}
