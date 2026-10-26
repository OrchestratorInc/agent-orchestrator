import { Stack } from "expo-router";
import { largeTitleScreenOptions, stackScreenOptions } from "./stackScreenOptions";
import { useTheme } from "./ThemeProvider";

/**
 * A board tab's own stack, holding just the tab's page.
 *
 * The stack is there for the header: a native one, with a large title on iOS
 * that collapses into the bar as the list scrolls and a top app bar on Android.
 * The page declares its own header items with `Stack.Toolbar`. Everything the
 * page opens is pushed on the root stack, as before, so it covers the tabs.
 */
export function TabStack({ screen, title }: { screen: string; title: string }) {
	const t = useTheme();
	return (
		<Stack screenOptions={{ ...stackScreenOptions(t), ...largeTitleScreenOptions() }}>
			<Stack.Screen name={screen} options={{ title }} />
		</Stack>
	);
}
