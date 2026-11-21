import type { NativeStackNavigationOptions } from "expo-router";
import { FlatBackButton } from "./FlatBackButton";

/**
 * The stack's back button: the app's flat chevron (FlatBackButton) in place of
 * the system's, which iOS 26 draws in a glass circle. It is a header item with
 * the shared glass background turned off; the system's own button is hidden.
 * The edge swipe back belongs to the navigation controller, not the button, so
 * it is unchanged.
 *
 * Shown wherever the stack can go back. A page that would show no system back
 * button (a modal's first page) opts out with `SYSTEM_BACK_OPTIONS`.
 */
export function headerBackOptions(): NativeStackNavigationOptions {
	return {
		headerBackVisible: false,
		unstable_headerLeftItems: ({ canGoBack }) => (canGoBack ? [{ type: "custom", element: <FlatBackButton />, hidesSharedBackground: true }] : []),
	};
}

/** For a page whose left side belongs to the system: a modal, or a page with no back. */
export const SYSTEM_BACK_OPTIONS: NativeStackNavigationOptions = { headerBackVisible: undefined, unstable_headerLeftItems: undefined };
