import { Platform } from "react-native";
import type { Theme } from "./theme";

export type TextSelectionColors = {
	selectionColor: string;
	cursorColor?: string;
	selectionHandleColor?: string;
};

/**
 * Selection colors for every TextInput.
 *
 * Android paints `selectionColor` as the highlight itself, at full opacity. The
 * accent is the same ink as the text, so an opaque accent hid the selected
 * characters behind a solid block. Android gets the translucent accent for the
 * highlight and keeps the solid accent for the cursor and drag handles, which
 * would otherwise inherit the translucent value. iOS applies its own alpha to
 * the highlight and uses `selectionColor` for the caret and handles, so it
 * keeps the solid accent.
 */
export function textSelectionColors(t: Theme): TextSelectionColors {
	if (Platform.OS === "android") {
		return { selectionColor: t.accentBorder, cursorColor: t.accent, selectionHandleColor: t.accent };
	}
	return { selectionColor: t.accent };
}
