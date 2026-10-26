import { StyleSheet } from "react-native";
import { rowDividerWidth } from "../divider";
import type { Theme } from "../theme";
import { radius, space, touchTarget, type } from "../tokens";
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
/** The pill's corner, and so also the radius of the glass drawn behind it: half its resting height, a capsule. */
export const COMPOSER_RADIUS = COMPOSER_HEIGHT / 2;

export const composerStyleRules = (t: Theme) => StyleSheet.create({
	dock: { paddingHorizontal: space.md, paddingTop: space.xs, gap: space.xs, backgroundColor: t.bgBase },
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
	// The native content-size event grows this from its one-line resting height;
	// at the cap, the multiline field scrolls while the controls remain in place.
	input: { fontFamily: "DMSans_400Regular", flex: 1, minHeight: COMPOSER_FIELD_HEIGHT, maxHeight: COMPOSER_FIELD_MAX_HEIGHT, color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: COMPOSER_LINE_HEIGHT, paddingVertical: space.md, textAlignVertical: "top" },
	// Clipped so the Android ripple keeps to the circle.
	send: { width: touchTarget, height: touchTarget, borderRadius: radius.pill, overflow: "hidden", alignItems: "center", justifyContent: "center", backgroundColor: t.accent },
	stop: { width: touchTarget, height: touchTarget, borderRadius: radius.pill, overflow: "hidden", alignItems: "center", justifyContent: "center", backgroundColor: t.bgSubtle, borderWidth: rowDividerWidth, borderColor: t.borderDefault },
	attachments: { gap: space.xs, paddingBottom: space.xs },
	// The chip is as tall as the remove button it ends in, so the whole button
	// is a touch target rather than a glyph with some slop around it.
	attachment: { maxWidth: 200, minHeight: touchTarget, flexDirection: "row", alignItems: "center", gap: space.xs, backgroundColor: t.bgElevated, borderRadius: radius.sm, borderCurve: "continuous", borderWidth: rowDividerWidth, borderColor: t.borderSubtle, paddingLeft: space.sm },
	attachmentImage: { width: 28, height: 28, borderRadius: radius.xs, backgroundColor: t.bgSubtle },
	attachmentName: { fontFamily: "DMSans_400Regular", flexShrink: 1, color: t.textSecondary, fontSize: type.footnote.fontSize },
	attachmentRemove: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" },
	error: { fontFamily: "DMSans_400Regular", color: t.red, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, marginBottom: space.xs, paddingHorizontal: space.hair },
	voice: { flexDirection: "row", alignItems: "center", gap: space.xs, backgroundColor: t.tintRed, borderRadius: radius.sm, borderCurve: "continuous", paddingHorizontal: space.sm, paddingVertical: space.xs, marginBottom: space.xs },
	voiceText: { fontFamily: "DMSans_400Regular", flex: 1, color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
});
