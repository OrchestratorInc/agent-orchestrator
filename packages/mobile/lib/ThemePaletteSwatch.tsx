import { StyleSheet, View } from "react-native";
import type { Theme } from "./theme";

/**
 * A theme drawn on its own canvas: its highlight, then the working, ready and
 * merged hues. Colours come from the swatch's theme, not the active one, because
 * each swatch previews a theme that usually isn't applied. The mobile twin of
 * desktop's settings/ThemePaletteSwatch.tsx, but drawn from the phone's derived
 * palette so the preview matches what the theme will look like here.
 */
export function ThemePaletteSwatch({ theme, border }: { theme: Theme; border: string }) {
	const hues = [theme.highlight, theme.orange, theme.green, theme.purple];
	return (
		<View accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={[styles.tile, { backgroundColor: theme.bgBase, borderColor: border }]}>
			{hues.map((color, index) => (
				<View key={index} style={[styles.dot, { backgroundColor: color }]} />
			))}
		</View>
	);
}

const styles = StyleSheet.create({
	tile: {
		width: 40,
		height: 26,
		borderRadius: 7,
		borderCurve: "continuous",
		borderWidth: StyleSheet.hairlineWidth,
		flexDirection: "row",
		alignItems: "flex-end",
		gap: 3,
		paddingHorizontal: 5,
		paddingBottom: 5,
	},
	dot: { width: 6, height: 6, borderRadius: 3 },
});
