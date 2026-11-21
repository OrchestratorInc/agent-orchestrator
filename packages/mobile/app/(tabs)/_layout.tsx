import { Stack } from "expo-router";

/**
 * The board: the Workers page alone. Projects, New task, Pull requests,
 * Notifications and Settings are reached from the sidebar, the desktop's left
 * sidebar presented as a drawer over every page (lib/sidebar), so the tab bar
 * that used to switch between Workers and Projects is gone.
 *
 * The group keeps its name so deep links and restored routes into the board
 * (`/`) resolve as before.
 */
export default function BoardLayout() {
	return (
		<Stack screenOptions={{ headerShown: false }}>
			<Stack.Screen name="(workers)" />
		</Stack>
	);
}
