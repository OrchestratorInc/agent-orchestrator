import { useEffect, type RefObject } from "react";
import type { BoardTabName } from "./board-tabs";

/**
 * Tapping the board tab already on screen scrolls its list back to the top.
 *
 * iOS's tab bar does this natively. Android's bar is the app's own
 * (`BoardTabBar`), so each tab's page registers its list here and the bar's
 * layout calls `reselectBoardTab` when the selected tab is tapped again.
 */
type ScrollableList = { scrollToOffset: (params: { offset: number; animated?: boolean }) => void };

const handlers = new Map<BoardTabName, () => void>();

export function reselectBoardTab(tab: BoardTabName): void {
	handlers.get(tab)?.();
}

/** Scrolls `list` to its top when `tab` is tapped while it is selected. */
export function useScrollToTopOnReselect(tab: BoardTabName, list: RefObject<ScrollableList | null>): void {
	useEffect(() => {
		const scroll = () => list.current?.scrollToOffset({ offset: 0, animated: true });
		handlers.set(tab, scroll);
		return () => {
			if (handlers.get(tab) === scroll) handlers.delete(tab);
		};
	}, [list, tab]);
}
