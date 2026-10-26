import { Stack } from "expo-router";
import { useTheme } from "../../lib/ThemeProvider";

// A link straight to a Settings page opens it over the Settings list, so its
// back control returns to the list. Without this, the page would be the only
// one in the sheet and back would close Settings.
export const unstable_settings = { initialRouteName: "index" };

export default function SettingsLayout() {
	const t = useTheme();

	return (
		<Stack
			screenOptions={{
				headerStyle: { backgroundColor: t.bgBase },
				headerTintColor: t.textSecondary,
				headerTitleStyle: { fontFamily: "Geist_600SemiBold", fontWeight: "600" },
				headerShadowVisible: false,
				contentStyle: { backgroundColor: t.bgBase },
			}}
		>
			<Stack.Screen name="index" options={{ headerShown: false }} />
			<Stack.Screen
				name="environments"
				options={{
					headerShown: false,
					animation: "slide_from_right",
					gestureEnabled: true,
					fullScreenGestureEnabled: true,
				}}
			/>
			<Stack.Screen
				name="theme"
				options={{
					headerShown: false,
					animation: "slide_from_right",
					gestureEnabled: true,
					fullScreenGestureEnabled: true,
				}}
			/>
		</Stack>
	);
}
