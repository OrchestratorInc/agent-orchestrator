import { useMemo } from "react";
import { Platform, StyleSheet, useWindowDimensions } from "react-native";
import { rowDividerWidth } from "../divider";
import type { Theme } from "../theme";
import { fontScaleCap, radius, space, touchTarget, type } from "../tokens";
import { composerGlassSupported } from "./composer-glass";

// The composer dock and pill shared by the session chat (ChatComposer) and the
// reviewer chat (ReviewerComposer), so the two cannot drift apart. Each composer
// spreads these into its own stylesheet and adds only what it alone draws.

/**
 * Resting panel height without quotes: one row of touch-target controls inside
 * the pill's inset. The input row grows up to COMPOSER_MAX_HEIGHT.
 */
export const COMPOSER_HEIGHT = touchTarget + space.xs * 2;
export const COMPOSER_FIELD_HEIGHT = 44;
/**
 * Where the pill stops growing and the field starts scrolling instead.
 *
 * Main's numbers, kept because they were tuned against the same transcript: about
 * six lines of a reply's worth of prompt, so the composer can never swallow the
 * conversation it is writing into.
 */
export const COMPOSER_MAX_HEIGHT = 150;
export const COMPOSER_FIELD_MAX_HEIGHT = 138;
export const COMPOSER_LINE_HEIGHT = type.subheadline.lineHeight;
/** Where the field's text stops growing with the reader's text size, as body copy does. */
export const COMPOSER_FONT_SCALE_CAP = fontScaleCap.body;

/**
 * The field's and its row's heights at the reader's text size. They are
 * points, which the text size does not reach, so at the largest sizes one line
 * and its padding outgrew them and the placeholder was cut top and bottom.
 * They grow with the text up to its cap. `fontScale` is the system's own, for
 * what has to be rebuilt when it changes.
 */
export function useComposerFieldSize() {
	const { fontScale } = useWindowDimensions();
	const scale = Math.min(Math.max(fontScale || 1, 1), COMPOSER_FONT_SCALE_CAP);
	return useMemo(() => ({
		fontScale,
		field: { minHeight: Math.round(COMPOSER_FIELD_HEIGHT * scale), maxHeight: Math.round(COMPOSER_FIELD_MAX_HEIGHT * scale) },
		row: { maxHeight: Math.round(COMPOSER_MAX_HEIGHT * scale) },
	}), [fontScale, scale]);
}
/**
 * The chat composer's filled controls, drawn this tall inside their touch
 * targets: the plus's circle, the model's capsule and the round button, so the
 * row's three shapes line up.
 */
export const COMPOSER_DISC = 34;
/** The pill's corner, and so also the radius of the glass drawn behind it: half its resting height, a capsule. */
export const COMPOSER_RADIUS = COMPOSER_HEIGHT / 2;

const ANDROID_TEXT_DROP = Platform.OS === "android" ? space.hair : 0;

export const composerStyleRules = (t: Theme) => StyleSheet.create({
	// A narrow gutter, as the system's own message fields keep: the pill is the
	// widest thing on the screen, and every point given to the margin is taken
	// from the line being written.
	dock: { paddingHorizontal: space.sm, paddingTop: space.xs, gap: space.xs, backgroundColor: t.bgBase },
	// One glass panel for selected quotes, attach, the field, dictation and the filled
	// control that commits. The pill's radius is half its resting height, so a
	// single line reads as a capsule; quotes and long replies grow a rounded panel —
	// no second border, no second surface.
	//
	// The fill goes transparent where the glass layer is drawing behind it: an
	// opaque pill under a material is the one arrangement that turns glass grey.
	//
	// Bottom-align the controls so attach, mic and send stay anchored while the
	// text field grows above them. The panel grows around both rows; the glass fills
	// that panel rather than receiving a separately measured height.
	composer: { minHeight: COMPOSER_HEIGHT, flexDirection: "column", paddingHorizontal: space.xs, paddingVertical: space.xs, backgroundColor: composerGlassSupported ? "transparent" : t.bgElevated, borderRadius: COMPOSER_RADIUS, borderCurve: "continuous" },
	composerRow: { maxHeight: COMPOSER_MAX_HEIGHT, flexDirection: "row", alignItems: "flex-end", gap: space.xxs },
	// A multiline field grows with its text from its one-line resting height; at
	// the cap it scrolls while the controls remain in place. No side padding of
	// its own: Android's text field brings some from the platform's theme, which
	// set the text further in than iOS's, and the row already insets the field.
	//
	// Android sets DM Sans high in its line, so the placeholder sat 2dp above
	// the plus and the mic; there the text starts that much lower, the field
	// the same height.
	input: { fontFamily: "DMSans_400Regular", flex: 1, minHeight: COMPOSER_FIELD_HEIGHT, maxHeight: COMPOSER_FIELD_MAX_HEIGHT, color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: COMPOSER_LINE_HEIGHT, paddingTop: space.md + ANDROID_TEXT_DROP, paddingBottom: space.md - ANDROID_TEXT_DROP, paddingHorizontal: 0, textAlignVertical: "top" },
	// iOS wraps the field in `HardwareReturn`, which then holds the field's place in
	// the row. A row of its own keeps the field's `flex: 1` on the horizontal axis,
	// so the field still sets its own height.
	inputSlot: { flex: 1, minWidth: 0, flexDirection: "row", alignItems: "flex-end" },
	// Clipped so the Android ripple keeps to the circle.
	send: { width: touchTarget, height: touchTarget, borderRadius: radius.pill, overflow: "hidden", alignItems: "center", justifyContent: "center", backgroundColor: t.accent },
	stop: { width: touchTarget, height: touchTarget, borderRadius: radius.pill, overflow: "hidden", alignItems: "center", justifyContent: "center", backgroundColor: t.bgSubtle, borderWidth: rowDividerWidth, borderColor: t.borderDefault },
	error: { fontFamily: "DMSans_400Regular", color: t.red, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, marginBottom: space.xs, paddingHorizontal: space.hair },
});
