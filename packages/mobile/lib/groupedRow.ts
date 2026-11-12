// An inset-grouped list drawn row by row in React Native, for lists that need
// a FlatList or SectionList (pull to refresh, long-press menus per row) rather
// than a native Form. Each row draws its own slice of the section's card.
import { groupedCorner } from "./tokens";

export type GroupedRowPosition = "only" | "leading" | "middle" | "trailing";

/** Where a row sits in its section, from its index and the section's length. */
export function groupedRowPosition(index: number, total: number): GroupedRowPosition {
	if (total <= 1) return "only";
	if (index === 0) return "leading";
	return index === total - 1 ? "trailing" : "middle";
}

/** The row's corners: the section's radius at its ends, the join radius between rows. */
export function groupedRowCorners(position: GroupedRowPosition, corner: { outer: number; inner: number } = groupedCorner) {
	const top = position === "only" || position === "leading" ? corner.outer : corner.inner;
	const bottom = position === "only" || position === "trailing" ? corner.outer : corner.inner;
	return { borderTopLeftRadius: top, borderTopRightRadius: top, borderBottomLeftRadius: bottom, borderBottomRightRadius: bottom };
}

/** Whether a row ends its section, so draws no divider (iOS) or gap (Android) under it. */
export function endsGroupedSection(position: GroupedRowPosition): boolean {
	return position === "only" || position === "trailing";
}
