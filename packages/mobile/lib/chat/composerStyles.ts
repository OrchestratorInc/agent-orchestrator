import { StyleSheet } from "react-native";
import type { Theme } from "../theme";
import { space, type } from "../tokens";
import { composerGlassSupported } from "./composer-glass";

// The composer dock and pill shared by the session chat (ChatComposer) and the
// reviewer chat (ReviewerComposer), so the two cannot drift apart. Each composer
// spreads these into its own stylesheet and adds only what it alone draws.

/** Resting panel height without quotes; the input row grows up to COMPOSER_MAX_HEIGHT. */
export const COMPOSER_HEIGHT = 56;
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
/** The pill's corner, and so also the radius of the glass drawn behind it. */
export const COMPOSER_RADIUS = 28;

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
	input: { fontFamily: "Geist_400Regular", flex: 1, minHeight: COMPOSER_FIELD_HEIGHT, maxHeight: COMPOSER_FIELD_MAX_HEIGHT, color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: COMPOSER_LINE_HEIGHT, paddingVertical: space.md, textAlignVertical: "top" },
	send: { width: 44, height: 44, borderRadius: 22, alignItems: "center", justifyContent: "center", backgroundColor: t.accent },
	stop: { width: 44, height: 44, borderRadius: 22, alignItems: "center", justifyContent: "center", backgroundColor: t.bgSubtle, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderDefault },
	attachments: { gap: space.xs, paddingBottom: space.xs },
	attachment: { maxWidth: 180, flexDirection: "row", alignItems: "center", gap: space.xs, backgroundColor: t.bgElevated, borderRadius: 8, borderWidth: 1, borderColor: t.borderSubtle, paddingHorizontal: space.sm, paddingVertical: space.xs },
	attachmentImage: { width: 28, height: 28, borderRadius: 4, backgroundColor: t.bgSubtle },
	attachmentName: { fontFamily: "Geist_400Regular", flexShrink: 1, color: t.textSecondary, fontSize: type.caption2.fontSize },
	error: { fontFamily: "Geist_400Regular", color: t.red, fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight, marginBottom: space.xs, paddingHorizontal: space.hair },
	voice: { flexDirection: "row", alignItems: "center", gap: space.xs, backgroundColor: t.tintRed, borderRadius: 8, paddingHorizontal: space.sm, paddingVertical: space.xs, marginBottom: space.xs },
	voiceText: { fontFamily: "Geist_400Regular", flex: 1, color: t.textSecondary, fontSize: type.caption2.fontSize },
});
