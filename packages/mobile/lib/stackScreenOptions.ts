import type { NativeStackNavigationOptions } from "expo-router";
import { Platform } from "react-native";
import type { Theme } from "./theme";

/**
 * iOS 26 draws a navigation bar as Liquid Glass with a scroll-edge blur under
 * it. A page that lays its scroll view out under a clear bar (`headerTransparent`
 * with no `headerStyle`) gets that blur instead of a painted slab.
 */
export const SYSTEM_GLASS_HEADER = Platform.OS === "ios" && parseInt(String(Platform.Version), 10) >= 26;

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
 * A board tab's header, spread over `stackScreenOptions`: no title, only the
 * page's bar items, so the list runs on seamlessly up to the bar. The screen's
 * `title` still names it for VoiceOver; only the bar's text goes.
 *
 * iOS 26 draws the bar as clear Liquid Glass over the list, with the system's
 * scroll-edge blur, so the rows scroll on under it. The page is then laid out
 * under the bar, so its first view must be the scroll view, with
 * `contentInsetAdjustmentBehavior="automatic"`. Earlier versions, and
 * Android's top app bar, keep the page-coloured bar from the stack defaults.
 */
export function untitledTabScreenOptions(): NativeStackNavigationOptions {
	return {
		headerTitle: "",
		...(Platform.OS === "ios" && parseInt(String(Platform.Version), 10) >= 26 ? { headerTransparent: true, headerStyle: undefined } : null),
	};
}
