import type { NativeStackNavigationOptions } from "expo-router";
import { Platform } from "react-native";
import type { Theme } from "./theme";

/**
 * The native-stack defaults every stack in the app shares: the root stack and
 * any stack nested in it (a tab's, Settings'). Spread them first and add what a
 * stack needs on top, so each one pushes, tints and titles its pages the same
 * way.
 *
 * No `animation`: Android then runs the system's own push transition, the one
 * every other app on the device uses, instead of a slide picked for it.
 *
 * The iOS bar is painted in the page's own background rather than the
 * navigation theme's `card` surface, so the header reads as part of the page
 * instead of a separate slab. It stays opaque: the page is laid out below the
 * bar, and a clear bar showed the page underneath through it for the length of
 * every push and pop, such as the board's own header behind a session's back
 * button. A large-title page sets `headerStyle: undefined` beside
 * `headerLargeTitleEnabled`, because iOS 26 hides a large title drawn over a set
 * background; native-stack then makes that bar transparent and lays the page out
 * under it, so its scroll view needs `contentInsetAdjustmentBehavior="automatic"`.
 * Android keeps the `card` surface behind its top app bar.
 */
export function stackScreenOptions(t: Theme): NativeStackNavigationOptions {
	return {
		...(Platform.OS === "ios" ? { headerStyle: { backgroundColor: t.bgBase } } : null),
		// The app's tint: the system back button and every native header item
		// draw in it, as bar buttons do in any app on either platform.
		headerTintColor: t.accent,
		headerTitleStyle: { fontFamily: "Geist_600SemiBold", fontWeight: "600" },
		headerShadowVisible: false,
		headerBackButtonDisplayMode: "minimal",
		contentStyle: { backgroundColor: t.bgBase },
	};
}
