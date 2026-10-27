import { Stack } from "expo-router";
import { stackScreenOptions, SYSTEM_GLASS_HEADER } from "../../lib/stackScreenOptions";
import { useTheme } from "../../lib/ThemeProvider";

/**
 * Notifications' own stack, holding just its page. On iOS the root stack
 * presents Notifications as a sheet, which draws no header of its own, so the
 * page's title and its Clear all item come from this stack's bar.
 *
 * The bar is Settings' sheet bar: an inline title, and on iOS 26 a clear bar
 * the list scrolls under. A large title in a sheet left the page blank under it.
 */
export default function NotificationsLayout() {
	const t = useTheme();
	return (
		<Stack screenOptions={{ ...stackScreenOptions(t), ...(SYSTEM_GLASS_HEADER ? { headerTransparent: true, headerStyle: undefined } : null) }}>
			<Stack.Screen name="index" options={{ title: "Notifications" }} />
		</Stack>
	);
}
