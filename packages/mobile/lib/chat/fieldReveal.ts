import { createContext } from "react";
import type { View } from "react-native";

/**
 * Brings a focused request-form field into view above the keyboard and the
 * composer. The timeline provides it: its list does not scroll a focused child
 * into view (`scrollsChildToFocus={false}`, which kept selectable text from
 * nudging the conversation), so a field asks for itself when it takes focus.
 */
export const RevealFocusedField = createContext<((field: View | null) => void) | undefined>(undefined);

/** Window-coordinate spans, in points. */
export type FieldRevealSpans = {
	fieldTop: number;
	fieldBottom: number;
	/** The top of the region the list shows, below the header and banners. */
	visibleTop: number;
	/** The bottom of that region, above the composer and the keyboard. */
	visibleBottom: number;
	/** The list's current scroll offset. */
	offset: number;
};

/**
 * The scroll offset that shows the whole field, moving the list as little as
 * possible, or undefined when it already shows: a field in view never moves the
 * conversation. A field taller than the region shows from its top.
 */
export function fieldRevealOffset({ fieldTop, fieldBottom, visibleTop, visibleBottom, offset }: FieldRevealSpans): number | undefined {
	if (fieldTop >= visibleTop && fieldBottom <= visibleBottom) return undefined;
	const below = fieldBottom - visibleBottom;
	const above = fieldTop - visibleTop;
	// Under the keyboard: up by what is hidden, but never past the field's top.
	const delta = below > 0 ? Math.min(below, above) : above;
	return delta === 0 ? undefined : offset + delta;
}
