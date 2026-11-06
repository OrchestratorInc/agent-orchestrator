import { useNavigation } from "expo-router";
import { CommonActions, getFocusedRouteNameFromRoute } from "expo-router/react-navigation";
import { useEffect } from "react";
import type { BoardTabName } from "./board-tabs";

/**
 * Turns the board under a page pushed on the root stack to `tab`, so leaving
 * the page lands on the list it belongs to.
 *
 * A cold link opens such a page over a board that has not picked a tab, which
 * shows Workers. Selecting the tab underneath, rather than intercepting back,
 * keeps every way out native: the system back button, the edge swipe, Android's
 * back and the back button's history menu all find the board on that tab.
 *
 * It goes through the board route's nested `screen` param because a board that
 * has never changed tab has no state in the root stack to target yet; the tab
 * navigator reads the param on its next render, without taking focus.
 */
export function useBoardTabBeneath(tab: BoardTabName): void {
	const navigation = useNavigation();
	useEffect(() => {
		const board = navigation.getState()?.routes.find((route) => route.name === "(tabs)");
		if (!board || getFocusedRouteNameFromRoute(board) === tab) return;
		navigation.dispatch({ ...CommonActions.setParams({ screen: tab }), source: board.key });
	}, [navigation, tab]);
}
