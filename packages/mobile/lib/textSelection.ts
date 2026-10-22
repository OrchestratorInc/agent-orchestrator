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
 * default accent is the same ink as the text, so an opaque fill hid the
 * selected characters behind a solid block. Android gets a translucent fill
 * and keeps the theme's solid highlight for the cursor and drag handles, which
 * would otherwise inherit the translucent value. iOS applies its own alpha to
 * the highlight and uses `selectionColor` for the caret and handles, so it
 * keeps the solid highlight.
 */
export function textSelectionColors(t: Theme): TextSelectionColors {
	if (Platform.OS === "android") {
		return { selectionColor: t.selection, cursorColor: t.highlight, selectionHandleColor: t.highlight };
	}
	return { selectionColor: t.highlight };
}
