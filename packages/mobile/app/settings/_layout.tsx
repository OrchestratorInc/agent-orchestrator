import { Stack } from "expo-router";
import { useTheme } from "../../lib/ThemeProvider";

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
		</Stack>
	);
}
