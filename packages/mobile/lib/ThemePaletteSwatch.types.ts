import type { Theme } from "./theme";

export type ThemePaletteSwatchProps = { theme: Theme };

/**
 * A theme's signature hues, in the order the swatch draws them: its highlight,
 * then the working, ready and merged colours. They come from the swatch's own
 * theme, not the active one, because each swatch previews a theme that usually
 * isn't applied; and from the phone's derived palette, so the preview matches
 * what the theme will look like here.
 */
export function paletteSwatchHues(theme: Theme): string[] {
	return [theme.highlight, theme.orange, theme.green, theme.purple];
}

/** Each hue's dot, and the gap between dots. */
export const SWATCH_DOT = 8;
export const SWATCH_GAP = 4;
