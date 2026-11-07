import { useMemo } from "react";
import { Platform, type PressableAndroidRippleConfig, type ViewStyle } from "react-native";
import { withAlpha } from "../../shared/theme/color";
import type { Theme } from "./theme";
import { useTheme } from "./ThemeProvider";
import { stateOpacity, touchTarget } from "./tokens";

/**
 * How a `Pressable` answers a touch, the way each platform's own controls do.
 *
 * iOS highlights a table row's background and fades a button; Android draws a
 * ripple state layer and never fades or scales. One call returns both halves,
 * so a call site spreads the ripple and adds the pressed style without
 * branching on the platform itself:
 *
 *   const feedback = usePressFeedback("row");
 *   <Pressable
 *     android_ripple={feedback.android_ripple}
 *     style={({ pressed }) => [styles.row, pressed && feedback.pressed]}
 *   />
 */

/**
 * What the finger lands on.
 *
 *  - `row`: a full-width list row. iOS fills it with the hover surface, the
 *    way a table cell highlights; Android ripples across it.
 *  - `button`: a control with its own bounds and usually a fill. iOS fades it;
 *    Android ripples inside its bounds, over the fill.
 *  - `icon`: a bare glyph with no container. iOS fades it; Android draws the
 *    Material icon-button ripple, an unbounded circle the size of the touch
 *    target, so it reads the same however small the glyph is.
 */
export type PressKind = "row" | "button" | "icon";

/**
 * The surface the ripple is drawn over.
 *
 *  - `neutral`: the theme's text colour at tint strength, which shows on any
 *    surface the theme draws.
 *  - `destructive`: the red tint, for a control that deletes.
 *  - `onAccent`: the ink of a filled accent control. A white ripple vanishes
 *    on the dark theme's near-white fill; the fill's own ink shows on both.
 */
export type PressTone = "neutral" | "destructive" | "onAccent";

export type PressFeedbackOptions = {
	tone?: PressTone;
	/** Android ripple radius. Defaults to half the touch target for `icon`, and to the view's bounds otherwise. */
	radius?: number;
	/**
	 * Draw the Android ripple over the content instead of behind it. Needed
	 * when an opaque child (an image, a card) covers the pressable's own
	 * background and would hide a ripple drawn there.
	 */
	foreground?: boolean;
};

export type PressFeedback = {
	/** Pass as the `Pressable`'s `android_ripple`. Undefined off Android. */
	android_ripple: PressableAndroidRippleConfig | undefined;
	/** Add to the style while pressed. Undefined on Android, where the ripple is the whole response. */
	pressed: ViewStyle | undefined;
};

// Material's pressed state layer is about a tenth of the content colour; ink on
// a filled accent needs a little more to read against the fill.
const ON_ACCENT_RIPPLE_ALPHA = 0.16;

/** The ripple colour for a tone, from the theme. */
export function rippleColor(t: Theme, tone: PressTone = "neutral"): string {
	if (tone === "destructive") return t.tintRed;
	if (tone === "onAccent") return withAlpha(t.onAccent, ON_ACCENT_RIPPLE_ALPHA);
	return t.accentTint;
}

export function pressFeedback(t: Theme, kind: PressKind, options: PressFeedbackOptions = {}): PressFeedback {
	if (Platform.OS === "android") {
		const borderless = kind === "icon";
		return {
			android_ripple: {
				color: rippleColor(t, options.tone),
				borderless,
				radius: options.radius ?? (borderless ? touchTarget / 2 : undefined),
				foreground: options.foreground,
			},
			pressed: undefined,
		};
	}
	return {
		android_ripple: undefined,
		pressed: kind === "row" ? { backgroundColor: t.bgElevatedHover } : { opacity: stateOpacity.pressed },
	};
}

/** `pressFeedback` for the current theme, stable across renders. */
export function usePressFeedback(kind: PressKind, options: PressFeedbackOptions = {}): PressFeedback {
	const t = useTheme();
	const { tone, radius, foreground } = options;
	return useMemo(() => pressFeedback(t, kind, { tone, radius, foreground }), [t, kind, tone, radius, foreground]);
}
