import { Stack } from "expo-router";
import { boardTitle } from "../../../lib/board-tabs";
import { largeTitleScreenOptions, stackScreenOptions } from "../../../lib/stackScreenOptions";
import { useTheme } from "../../../lib/ThemeProvider";

/**
 * Home and the Workers board. Home is the sidebar, which draws its own header
 * (the Orchestrator brand) as the desktop's does; the board keeps its native
 * header with the large title.
 */
export default function HomeLayout() {
	const t = useTheme();
	return (
		<Stack screenOptions={{ ...stackScreenOptions(t), ...largeTitleScreenOptions() }}>
			<Stack.Screen name="index" options={{ title: "Home", headerShown: false }} />
			<Stack.Screen name="board" options={{ title: boardTitle("(workers)") }} />
		</Stack>
	);
}
