import { StyleSheet, View } from "react-native";
import { rowDividerWidth } from "../divider";
import { useTheme } from "../ThemeProvider";

/**
 * Android's stand-in for the composer's Liquid Glass: a sheet one step off the
 * page with a hairline rim, drawn behind the row exactly where iOS draws its
 * material, so every composer, chip and pill takes the same shape on both.
 *
 * The step is what the controls need. The plus's circle and the model's capsule
 * are filled in the card colour, and on a pill filled in that same colour they
 * vanished, leaving bare glyphs; on the darker sheet they read as the circles
 * and capsule iOS draws, and the rim keeps the pill's edge as the conversation
 * fades out under the dock.
 */
export const composerGlassSupported = true;

export function ComposerGlass({ radius }: { radius: number }) {
	const t = useTheme();
	return (
		<View
			pointerEvents="none"
			style={[styles.sheet, { borderRadius: radius, backgroundColor: t.bgSurface, borderColor: t.borderDefault }]}
		/>
	);
}

const styles = StyleSheet.create({
	sheet: { position: "absolute", top: 0, right: 0, bottom: 0, left: 0, borderWidth: rowDividerWidth, borderCurve: "continuous" },
});
