import { View } from "react-native";
import { paletteSwatchHues, SWATCH_DOT, SWATCH_GAP, type ThemePaletteSwatchProps } from "./ThemePaletteSwatch.types";

export type { ThemePaletteSwatchProps } from "./ThemePaletteSwatch.types";

/**
 * A theme's signature hues as a row of dots before its name, at the start of
 * its row in the theme list. The name beside it is what VoiceOver reads; the
 * dots are decoration.
 */
export function ThemePaletteSwatch({ theme }: ThemePaletteSwatchProps) {
	return (
		<View style={{ flexDirection: "row", gap: SWATCH_GAP }} accessibilityElementsHidden importantForAccessibility="no-hide-descendants">
			{paletteSwatchHues(theme).map((color, index) => (
				<View key={index} style={{ width: SWATCH_DOT, height: SWATCH_DOT, borderRadius: SWATCH_DOT / 2, backgroundColor: color }} />
			))}
		</View>
	);
}
