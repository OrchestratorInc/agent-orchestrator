import { Stack, useRouter } from "expo-router";
import { useMemo } from "react";
import { Platform } from "react-native";
import { backOr } from "./backNavigation";

/**
 * The back button for the first page of a stack pushed over the board:
 * Settings, Notifications, Pull Requests. Each holds a stack of its own, and
 * UIKit draws a back button only for a page with another under it in the same
 * navigation controller, so on iOS these pages would show none. Android's
 * react-native-screens draws one from the parent stack itself, so this adds
 * nothing there.
 *
 * It sits where the system's own does, as a bare chevron (the app hides back
 * titles), and pops the page with the same push transition run backwards.
 */
export function StackRootBack() {
	const router = useRouter();
	return useMemo(() => (Platform.OS === "ios" ? (
		<Stack.Toolbar placement="left">
			<Stack.Toolbar.Button icon="chevron.backward" accessibilityLabel="Back" onPress={() => backOr(router)} />
		</Stack.Toolbar>
	) : null), [router]);
}
