import { StyleSheet, View } from "react-native";

import { freshnessColor, type FreshnessTone } from "./machineFreshness";
import { useTheme } from "./ThemeProvider";

const SIZE = 7;

/**
 * The mark a machine wears when its rows may not be current: amber while it
 * lags, red once it can't be reached. A live machine wears nothing. A picture
 * only; the label beside it carries the words for screen readers.
 */
export function FreshnessDot({ tone, testID }: { tone: FreshnessTone | null; testID?: string }) {
	const t = useTheme();
	if (!tone) return null;
	return (
		<View
			testID={testID}
			accessibilityElementsHidden
			importantForAccessibility="no-hide-descendants"
			style={[styles.dot, { backgroundColor: freshnessColor(t, tone) }]}
		/>
	);
}

const styles = StyleSheet.create({
	dot: { width: SIZE, height: SIZE, borderRadius: SIZE / 2, flexShrink: 0 },
});
