import { Stack, useIsFocused, useRoute } from "expo-router";
import { useNavigationState } from "expo-router/react-navigation";
import { stackScreenOptions, untitledTabScreenOptions } from "./stackScreenOptions";
import { FrozenAppScope } from "./store";
import { useTheme } from "./ThemeProvider";

/**
 * A board tab's own stack, holding just the tab's page.
 *
 * The stack is there for the header: a native bar with no title, holding only
 * the header items the page declares with `Stack.Toolbar`, so the list reads
 * as one seamless page under it. Everything the
 * page opens is pushed on the root stack, as before, so it covers the tabs.
 *
 * Every tab stays mounted, so a tab that is not selected is frozen at the state
 * it last showed rather than re-rendering its board on every poll.
 */
export function TabStack({ screen, title }: { screen: string; title: string }) {
	const t = useTheme();
	const route = useRoute();
	const selected = useNavigationState((state) => state.routes[state.index]?.key === route.key);
	// Focus turns over in the same render as the tab does, so the page has the
	// latest state before its focus effects run; the selection only reaches this
	// a commit later. Selection keeps the tab live under a page pushed over it.
	const live = useIsFocused() || selected;
	return (
		<FrozenAppScope frozen={!live}>
			<Stack screenOptions={{ ...stackScreenOptions(t), ...untitledTabScreenOptions() }}>
				<Stack.Screen name={screen} options={{ title }} />
			</Stack>
		</FrozenAppScope>
	);
}
