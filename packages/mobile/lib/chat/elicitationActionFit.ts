import { useState } from "react";
import type { LayoutChangeEvent } from "react-native";

type ContentLayoutEvent = { nativeEvent: { width: number } };

/**
 * Keeps a native action button inside the row it sits in. A host that matches
 * its content measures the button with no width limit, so a decision longer
 * than the row would run past the card on one line. A hidden copy of the
 * button, laid across the row, reports both widths: while the label fits, the
 * button keeps its own size; once it doesn't, the button is held to the row
 * and its label wraps.
 */
export function useActionFit(label: string) {
	const [natural, setNatural] = useState<{ label: string; width: number }>();
	const [row, setRow] = useState(0);
	// A width measured for an earlier label says nothing about this one.
	const labelWidth = natural?.label === label ? natural.width : undefined;
	return {
		/** The width to hold the button to, or undefined while its label fits the row. */
		cap: labelWidth !== undefined && row > 0 && labelWidth > row + 0.5 ? Math.floor(row) : undefined,
		/** For the hidden copy's host, which spans the row. */
		onRowLayout: (event: LayoutChangeEvent) => setRow(event.nativeEvent.layout.width),
		/** For the hidden copy's content, measured at the label's natural width. */
		onLabelLayout: (event: ContentLayoutEvent) => {
			const width = event.nativeEvent.width;
			setNatural((old) => old?.label === label && old.width === width ? old : { label, width });
		},
	};
}
