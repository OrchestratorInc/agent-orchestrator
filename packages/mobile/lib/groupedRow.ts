// The shape of an inset-grouped list, row by row: where a row sits in its
// section and the corners that follows from. The React Native lists that need
// a FlatList or SectionList (the pull-request list) and Android's Compose
// grouped lists (composeGroupedList) both draw from it, so they keep one shape.
import { groupedCorner } from "./tokens";

export type GroupedRowPosition = "only" | "leading" | "middle" | "trailing";

/** Where a row sits in its section, from its index and the section's length. */
export function groupedRowPosition(index: number, total: number): GroupedRowPosition {
	if (total <= 1) return "only";
	if (index === 0) return "leading";
	return index === total - 1 ? "trailing" : "middle";
}

/** A row's top and bottom radius: the section's at its ends, the join radius between rows. */
export function groupedRowRadii(position: GroupedRowPosition, corner: { outer: number; inner: number } = groupedCorner): { top: number; bottom: number } {
	return {
		top: position === "only" || position === "leading" ? corner.outer : corner.inner,
		bottom: position === "only" || position === "trailing" ? corner.outer : corner.inner,
	};
}

/** The row's corners as React Native border radii. */
export function groupedRowCorners(position: GroupedRowPosition, corner: { outer: number; inner: number } = groupedCorner) {
	const { top, bottom } = groupedRowRadii(position, corner);
	return { borderTopLeftRadius: top, borderTopRightRadius: top, borderBottomLeftRadius: bottom, borderBottomRightRadius: bottom };
}

/** Whether a row ends its section, so draws no divider (iOS) or gap (Android) under it. */
export function endsGroupedSection(position: GroupedRowPosition): boolean {
	return position === "only" || position === "trailing";
}
