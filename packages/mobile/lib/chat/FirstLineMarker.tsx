import type { ReactNode } from "react";
import { StyleSheet, Text, View, type StyleProp, type TextStyle, type ViewStyle } from "react-native";

/**
 * A drawn marker (a bullet, a task box) held level with the first line of the
 * text beside it. The column is one line tall, measured by an empty line set in
 * that text's own style, so it grows with the system font size exactly as the
 * text does (Android's non-linear scale included, which a multiple of
 * `fontScale` would miss), and the marker stays centred on the line.
 *
 * Give the column its width through `style`; the empty line has none.
 */
export function FirstLineMarker({ lineStyle, style, children }: { lineStyle: StyleProp<TextStyle>; style: StyleProp<ViewStyle>; children: ReactNode }) {
	return <View style={[styles.column, style]}>
		<Text accessible={false} accessibilityElementsHidden importantForAccessibility="no" style={lineStyle}>{"​"}</Text>
		<View style={styles.marker}>{children}</View>
	</View>;
}

const styles = StyleSheet.create({
	column: { flexShrink: 0 },
	marker: { position: "absolute", top: 0, right: 0, bottom: 0, left: 0, alignItems: "flex-end", justifyContent: "center" },
});
