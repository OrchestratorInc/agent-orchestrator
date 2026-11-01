import type { NativeStackNavigationOptions } from "expo-router";

/** The stack's back button: iOS draws its own chevron, so nothing to add. */
export function headerBackOptions(): NativeStackNavigationOptions {
	return {};
}
