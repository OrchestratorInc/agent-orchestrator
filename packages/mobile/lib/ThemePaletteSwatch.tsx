import { StyleSheet, View } from "react-native";
import type { Theme } from "./theme";

/**
 * A theme's signature hues as a row of dots before its name: its highlight,
 * then the working, ready and merged colours. Colours come from the swatch's
 * theme, not the active one, because each swatch previews a theme that usually
 * isn't applied. Drawn from the phone's derived palette so the preview matches
 * what the theme will look like here.
 */
export function ThemePaletteSwatch({ theme }: { theme: Theme }) {
	const hues = [theme.highlight, theme.orange, theme.green, theme.purple];
	return (
		<View accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={styles.dots}>
			{hues.map((color, index) => (
				<View key={index} style={[styles.dot, { backgroundColor: color }]} />
			))}
		</View>
	);
}

const styles = StyleSheet.create({
	dots: { flexDirection: "row", alignItems: "center", gap: 4 },
	dot: { width: 8, height: 8, borderRadius: 4 },
});
