import { Platform } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

/**
 * How far above a board tab's bottom edge a control floating over the page has
 * to sit to stay clear of the tab bar.
 *
 * iOS lays each tab's page out under the tab bar, and NativeTabs gives every
 * tab a safe-area provider of its own, so the bottom inset read inside a tab
 * already includes the bar and the home indicator below it. A list with
 * `contentInsetAdjustmentBehavior="automatic"` is inset by the same amount, so
 * it only pads for what floats over it.
 *
 * Android ends the page at the top of the navigation bar, which keeps clear of
 * the system bar itself, so there is nothing left to clear. The insets the page
 * reads there are still the window's, which is why this does not return them.
 * The keyboard is measured from the window's bottom edge rather than the
 * page's, so anything riding it also needs `useTabPageBottomGap`.
 */
export function useTabBarInset(): number {
	const insets = useSafeAreaInsets();
	return Platform.OS === "android" ? 0 : insets.bottom;
}
