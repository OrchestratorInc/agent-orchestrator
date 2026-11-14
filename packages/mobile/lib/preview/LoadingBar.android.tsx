import { Host, LinearProgressIndicator } from "@expo/ui/jetpack-compose";
import { fillMaxWidth } from "@expo/ui/jetpack-compose/modifiers";
import { StyleSheet, View } from "react-native";
import { useTheme, useThemeState } from "../ThemeProvider";
import { loadProgressPercent } from "./loadProgress";
import type { LoadingBarProps } from "./LoadingBar.types";

export type { LoadingBarProps } from "./LoadingBar.types";

/**
 * A page load as Material 3's linear progress indicator along the top edge:
 * determinate while the load reports how far it has come, Compose's own
 * indeterminate sweep while it does not.
 */
export function LoadingBar({ loading, progress, accessibilityLabel }: LoadingBarProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	if (!loading) return null;
	return (
		<View
			accessible
			accessibilityRole="progressbar"
			accessibilityLabel={accessibilityLabel}
			accessibilityState={{ busy: true }}
			accessibilityValue={progress === undefined ? undefined : { min: 0, max: 100, now: loadProgressPercent(progress) }}
			pointerEvents="none"
			style={styles.bar}
		>
			<Host matchContents={{ vertical: true }} colorScheme={scheme}>
				<LinearProgressIndicator progress={progress} color={t.accent} trackColor={t.accentTint} modifiers={[fillMaxWidth()]} />
			</Host>
		</View>
	);
}

const styles = StyleSheet.create({
	bar: { position: "absolute", top: 0, left: 0, right: 0 },
});
