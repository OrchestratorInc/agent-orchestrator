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
 * button. A large-title page spreads `largeTitleScreenOptions()` on top.
 * Android keeps the `card` surface behind its top app bar.
 */
export function stackScreenOptions(t: Theme): NativeStackNavigationOptions {
	return {
		...(Platform.OS === "ios" ? { headerStyle: { backgroundColor: t.bgBase } } : null),
		// The app's tint: the system back button and every native header item
		// draw in it, as bar buttons do in any app on either platform.
		headerTintColor: t.accent,
		// The system face at T3 Code's heavy weight: a navigation title is the
		// platform's chrome, and the app's own family is for what sits under it.
		headerTitleStyle: { fontSize: 17, fontWeight: "800" },
		headerShadowVisible: false,
		headerBackButtonDisplayMode: "minimal",
		contentStyle: { backgroundColor: t.bgBase },
	};
}

/**
 * A large-title page's header, spread over `stackScreenOptions`: the board
 * tabs' roots. iOS only; Android has no large title and keeps its top app bar.
 *
 * iOS 26 hides a large title drawn over a set background, so there the bar
 * keeps none: native-stack makes it transparent, and the system's scroll-edge
 * effect keeps the collapsed title legible over the list. Earlier versions have
 * no such effect, so their bar keeps the page's own colour. A large-title bar is
 * translucent either way and the page is laid out under it, so its first view
 * must be the scroll view, with `contentInsetAdjustmentBehavior="automatic"`;
 * that is also the view the title collapses with.
 */
export function largeTitleScreenOptions(): NativeStackNavigationOptions {
	if (Platform.OS !== "ios") return {};
	return {
		headerLargeTitleEnabled: true,
		...(parseInt(String(Platform.Version), 10) >= 26 ? { headerStyle: undefined } : null),
		headerLargeTitleShadowVisible: false,
		headerLargeTitleStyle: { fontWeight: "800" },
	};
}
