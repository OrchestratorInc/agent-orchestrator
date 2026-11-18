import type { Theme } from "./theme";

export type RefreshControlColors = {
	tintColor: string;
	colors: string[];
	progressBackgroundColor: string;
};

/**
 * A pull-to-refresh spinner in the theme's colours, spread onto a
 * `RefreshControl`. iOS tints its spinner. Android ignores the tint: Material's
 * spinner takes its arc colours and the disc behind it separately, and without
 * them stays on the system's light disc even in a dark theme.
 */
export function refreshControlColors(t: Theme): RefreshControlColors {
	return { tintColor: t.accent, colors: [t.accent], progressBackgroundColor: t.bgElevated };
}
