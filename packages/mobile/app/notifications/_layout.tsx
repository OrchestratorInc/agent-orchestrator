import { Stack } from "expo-router";
import { largeTitleScreenOptions, stackScreenOptions } from "../../lib/stackScreenOptions";
import { useTheme } from "../../lib/ThemeProvider";

/**
 * Notifications' own stack, holding just its page. On iOS the root stack
 * presents Notifications as a sheet, which draws no header of its own, so the
 * page's title and its Clear all item come from this stack's bar.
 */
export default function NotificationsLayout() {
	const t = useTheme();
	return (
		<Stack screenOptions={{ ...stackScreenOptions(t), ...largeTitleScreenOptions() }}>
			<Stack.Screen name="index" options={{ title: "Notifications" }} />
		</Stack>
	);
}
