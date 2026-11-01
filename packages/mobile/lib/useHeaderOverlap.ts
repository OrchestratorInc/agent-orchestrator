import { useHeaderHeight } from "expo-router/react-navigation";
import { Platform } from "react-native";

/**
 * How much of a board tab's page its header covers.
 *
 * iOS lays the page out under the large-title bar, which is translucent, so the
 * bar and the status bar above it cover the top of the page. A scroll view with
 * `contentInsetAdjustmentBehavior="automatic"` insets its content by that much
 * already; anything pinned over the page has to start below it. Android's top
 * app bar sits above the page and covers none of it.
 *
 * The height follows the large title as it collapses into the bar.
 */
export function useHeaderOverlap(): number {
	const headerHeight = useHeaderHeight();
	return Platform.OS === "ios" ? headerHeight : 0;
}
