import { Feather } from "./icons";
import { Pressable, StyleSheet } from "react-native";
import type { SpawnFabProps } from "./spawn-fab";
import { useTheme } from "./ThemeProvider";
import { iconSize, radius } from "./tokens";

/** Matches the spawn button in `worker-dock.android.tsx`. */
export function SpawnFab({ onPress, testID }: SpawnFabProps) {
	const t = useTheme();
	return (
		<Pressable
			testID={testID}
			accessibilityRole="button"
			accessibilityLabel="Spawn worker"
			android_ripple={{ color: t.accentTint, borderless: true, radius: 26 }}
			onPress={onPress}
			style={({ pressed }) => [
				styles.action,
				{ backgroundColor: pressed ? t.accentTint : t.bgElevatedHover, borderColor: t.borderStrong },
			]}
		>
			<Feather name="plus" size={iconSize.lg} color={t.textPrimary} />
		</Pressable>
	);
}

const styles = StyleSheet.create({
	action: {
		width: 52,
		height: 52,
		borderRadius: radius.pill, borderCurve: "continuous",
		borderWidth: StyleSheet.hairlineWidth,
		alignItems: "center",
		justifyContent: "center",
		overflow: "hidden",
	},
});
