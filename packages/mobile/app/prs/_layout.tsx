import { Stack } from "expo-router";
import { stackScreenOptions, SYSTEM_GLASS_HEADER } from "../../lib/stackScreenOptions";
import { useTheme } from "../../lib/ThemeProvider";

/**
 * Pull Requests' own stack, holding just its page. The root stack pushes it
 * with no header of its own, as it does Notifications, so the page's title and
 * its back button come from this stack's bar.
 *
 * The bar is Settings' bar: an inline title, and on iOS 26 a clear bar the
 * list scrolls under.
 */
export default function PRsLayout() {
	const t = useTheme();
	return (
		<Stack screenOptions={{ ...stackScreenOptions(t), ...(SYSTEM_GLASS_HEADER ? { headerTransparent: true, headerStyle: undefined } : null) }}>
			<Stack.Screen name="index" options={{ title: "Pull Requests" }} />
		</Stack>
	);
}
