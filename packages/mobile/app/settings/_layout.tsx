import { Stack } from "expo-router";
import { stackScreenOptions } from "../../lib/stackScreenOptions";
import { useTheme } from "../../lib/ThemeProvider";

// A link straight to a Settings page opens it over the Settings list, so its
// back button returns to the list. Without this, the page would be the only
// one in the stack and back would close Settings.
export const unstable_settings = { initialRouteName: "index" };

export default function SettingsLayout() {
	const t = useTheme();

	return (
		<Stack screenOptions={stackScreenOptions(t)}>
			<Stack.Screen name="index" options={{ title: "Settings" }} />
			<Stack.Screen name="environments" options={{ title: "Environments", fullScreenGestureEnabled: true }} />
			<Stack.Screen name="theme" options={{ title: "Theme", fullScreenGestureEnabled: true }} />
		</Stack>
	);
}
