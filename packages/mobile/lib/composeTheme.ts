import type { Theme } from "./theme";
import { font, radius } from "./tokens";

/**
 * The app's theme for Jetpack Compose, handed to every `@expo/ui` Host on
 * Android (see the `setAppTheme` patch to `@expo/ui`).
 *
 * Without it a Host takes Material's dynamic colours, which follow the
 * wallpaper: a lavender focus ring on a text field, a green switch, a pink
 * menu, beside React Native views drawn in this palette. Each Material role
 * names the surface or ink this palette already uses for the same job, so a
 * Compose control and its React Native neighbour read as one app.
 *
 * Colours stay strings here; the caller converts them for the bridge. Free of
 * React Native imports so the mapping is testable under Node.
 */
export type ComposeTheme = {
	dark: boolean;
	colors: Record<string, string>;
	fonts: { regular: string; medium: string; semibold: string };
	extraSmallCorner: number;
};

export function composeThemeFor(t: Theme, dark: boolean): ComposeTheme {
	return {
		dark,
		colors: {
			// Hue-less emphasis, as the app's filled buttons draw it.
			primary: t.accent,
			onPrimary: t.onAccent,
			primaryContainer: t.bgElevatedHover,
			onPrimaryContainer: t.textPrimary,
			// Selected segments, chips and the navigation indicator.
			secondary: t.textSecondary,
			onSecondary: t.bgBase,
			secondaryContainer: t.bgElevatedHover,
			onSecondaryContainer: t.textPrimary,
			// The one hue: links, selection and the cursor.
			tertiary: t.highlight,
			onTertiary: t.onAccent,
			background: t.bgBase,
			onBackground: t.textPrimary,
			surface: t.bgBase,
			onSurface: t.textPrimary,
			surfaceVariant: t.bgElevated,
			onSurfaceVariant: t.textSecondary,
			surfaceTint: t.accent,
			inverseSurface: t.textPrimary,
			inverseOnSurface: t.bgBase,
			error: t.red,
			onError: t.onDestructive,
			errorContainer: t.tintRed,
			onErrorContainer: t.red,
			// Field and switch borders need to read at rest; the faint text step does.
			outline: t.textFaint,
			outlineVariant: t.borderDefault,
			scrim: t.scrim,
			// Page, then sheet, then card, then the card's raised step.
			surfaceDim: t.bgBase,
			surfaceBright: t.bgElevatedHover,
			surfaceContainerLowest: t.bgBase,
			surfaceContainerLow: t.bgSurface,
			surfaceContainer: t.bgSurface,
			surfaceContainerHigh: t.bgElevated,
			surfaceContainerHighest: t.bgElevatedHover,
		},
		fonts: { regular: font.regular, medium: font.medium, semibold: font.semibold },
		// Menus, outlined fields and snackbars at the app's control radius
		// rather than Material's 4dp.
		extraSmallCorner: radius.md,
	};
}
