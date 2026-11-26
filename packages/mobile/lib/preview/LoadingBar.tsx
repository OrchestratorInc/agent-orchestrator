import { Host, ProgressView } from "@expo/ui/swift-ui";
import { progressViewStyle, tint } from "@expo/ui/swift-ui/modifiers";
import { useEffect, useState } from "react";
import { StyleSheet, View } from "react-native";
import { useTheme, useThemeState } from "../ThemeProvider";
import { estimatedLoadProgress, loadProgressPercent } from "./loadProgress";
import type { LoadingBarProps } from "./LoadingBar.types";

export type { LoadingBarProps } from "./LoadingBar.types";

/** How often an unreported load's estimate moves on. */
const ESTIMATE_TICK_MS = 120;

/**
 * A page load as SwiftUI's linear ProgressView along the top edge, as Safari
 * draws one. iOS gives the linear style no indeterminate animation, so a load
 * that reports no progress shows an estimate that slows as it nears the end,
 * and the bar leaves when the load does.
 */
export function LoadingBar({ loading, progress, accessibilityLabel }: LoadingBarProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const elapsed = useElapsed(loading && progress === undefined);
	if (!loading) return null;
	const value = progress ?? estimatedLoadProgress(elapsed);
	return (
		<View
			accessible
			accessibilityRole="progressbar"
			accessibilityLabel={accessibilityLabel}
			accessibilityValue={{ min: 0, max: 100, now: loadProgressPercent(value) }}
			pointerEvents="none"
			style={styles.bar}
		>
			<Host matchContents={{ vertical: true }} colorScheme={scheme}>
				<ProgressView value={value} modifiers={[progressViewStyle("linear"), tint(t.accent)]} />
			</Host>
		</View>
	);
}

function useElapsed(active: boolean): number {
	const [elapsed, setElapsed] = useState(0);
	useEffect(() => {
		if (!active) return;
		const startedAt = Date.now();
		const timer = setInterval(() => setElapsed(Date.now() - startedAt), ESTIMATE_TICK_MS);
		return () => {
			clearInterval(timer);
			setElapsed(0);
		};
	}, [active]);
	return elapsed;
}

const styles = StyleSheet.create({
	bar: { position: "absolute", top: 0, left: 0, right: 0 },
});
