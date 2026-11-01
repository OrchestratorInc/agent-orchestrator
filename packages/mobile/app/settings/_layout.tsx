import { Stack } from "expo-router";
import { Platform } from "react-native";
import { stackScreenOptions, SYSTEM_GLASS_HEADER } from "../../lib/stackScreenOptions";
import { useTheme } from "../../lib/ThemeProvider";

// A link straight to a Settings page opens it over the Settings list, so its
// back button returns to the list. Without this, the page would be the only
// one in the stack and back would leave Settings. Android's list is a tab, so
// a page opens alone over it there, and back returns to the tab.
export const unstable_settings = Platform.OS === "ios" ? { initialRouteName: "index" } : {};

// Settings is a page pushed over the board, like every other page, and its
// own pages push and go back the same way, with the system's edge swipe.
// On iOS 26 it paints no bar of its own: the bar is transparent, each page (a
// SwiftUI Form or an automatically inset scroll view) lays out under it, and
// the system blurs whatever scrolls beneath the title.
export default function SettingsLayout() {
	const t = useTheme();

	return (
		<Stack screenOptions={{ ...stackScreenOptions(t), ...(SYSTEM_GLASS_HEADER ? { headerTransparent: true, headerStyle: undefined } : null) }}>
			<Stack.Screen name="index" options={{ title: "Settings" }} />
			<Stack.Screen name="environments" options={{ title: "Machines" }} />
			<Stack.Screen name="theme" options={{ title: "Theme" }} />
			{/* A machine's page renames itself to the machine once it loads. */}
			<Stack.Screen name="machine/[id]/index" options={{ title: "Machine" }} />
			<Stack.Screen name="machine/[id]/edit" options={{ title: "Edit machine" }} />
		</Stack>
	);
}
