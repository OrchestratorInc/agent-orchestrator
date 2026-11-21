import type { NativeStackNavigationOptions } from "expo-router";
import { FlatBackButton } from "./FlatBackButton";

/**
 * Android's back button: the app's flat chevron (FlatBackButton), the same one
 * iOS and the conversation's top bar draw, rather than Material's arrow. A
 * native back image did not load from the dev server and left the bar with no
 * way back, so the bar draws it.
 */
export function headerBackOptions(): NativeStackNavigationOptions {
	return { headerLeft: ({ canGoBack }) => (canGoBack ? <FlatBackButton /> : null) };
}

/** For a page whose left side belongs to the system: a modal, or a page with no back. */
export const SYSTEM_BACK_OPTIONS: NativeStackNavigationOptions = { headerLeft: undefined };
