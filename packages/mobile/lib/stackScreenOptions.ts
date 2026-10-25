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
 * iOS gets a transparent bar rather than the default opaque one, so a page's
 * header is its own background with the controls on it, as on iOS 26, rather
 * than a separate slab. The page is still laid out below the bar, so nothing
 * draws under it. A large-title stack is the exception: native-stack makes that
 * bar translucent and lays the page out under it, so its scroll view needs
 * `contentInsetAdjustmentBehavior="automatic"` to start below the title.
 * Android keeps the navigation theme's `card` surface behind its top app bar.
 */
export function stackScreenOptions(t: Theme): NativeStackNavigationOptions {
	return {
		...(Platform.OS === "ios" ? { headerStyle: { backgroundColor: "transparent" } } : null),
		// The app's tint: the system back button and every native header item
		// draw in it, as bar buttons do in any app on either platform.
		headerTintColor: t.accent,
		headerTitleStyle: { fontFamily: "Geist_600SemiBold", fontWeight: "600" },
		headerShadowVisible: false,
		headerBackButtonDisplayMode: "minimal",
		contentStyle: { backgroundColor: t.bgBase },
	};
}
