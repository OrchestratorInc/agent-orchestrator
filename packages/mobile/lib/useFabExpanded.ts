import { useCallback, useRef, useState } from "react";
import { Platform, type NativeScrollEvent, type NativeSyntheticEvent } from "react-native";

/** How far the list moves in one direction before the button changes shape. */
const TRAVEL = 12;

/**
 * Whether an extended FAB shows its label, from how the list under it moved.
 *
 * Material's extended FAB folds to its icon while the user scrolls down to
 * read, and opens again on any scroll up or back at the top, so the label is
 * there whenever they are likely to reach for it. Returns the next state, or
 * the current one when the move is too small to call.
 */
export function nextFabExpanded(expanded: boolean, previousY: number, y: number): boolean {
	if (y <= TRAVEL) return true;
	const dy = y - previousY;
	if (dy > TRAVEL) return false;
	if (dy < -TRAVEL) return true;
	return expanded;
}

/**
 * The FAB's label state and the list's `onScroll` that drives it. Android
 * only: iOS draws no FAB (see AddFab), so it gets no handler and no renders.
 */
export function useFabExpanded(): {
	expanded: boolean;
	onScroll: ((event: NativeSyntheticEvent<NativeScrollEvent>) => void) | undefined;
} {
	const [expanded, setExpanded] = useState(true);
	const anchor = useRef(0);
	const onScroll = useCallback((event: NativeSyntheticEvent<NativeScrollEvent>) => {
		const y = event.nativeEvent.contentOffset.y;
		setExpanded((current) => {
			const next = nextFabExpanded(current, anchor.current, y);
			// Measure the next move from where this one settled or turned.
			if (next !== current || Math.abs(y - anchor.current) > TRAVEL) anchor.current = y;
			return next;
		});
	}, []);
	return { expanded, onScroll: Platform.OS === "android" ? onScroll : undefined };
}
