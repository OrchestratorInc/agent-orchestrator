import { Host } from "@expo/ui";
import { Button, Image } from "@expo/ui/swift-ui";
import { accessibilityLabel, buttonBorderShape, buttonStyle, controlSize, font, tint } from "@expo/ui/swift-ui/modifiers";
import { Platform, StyleSheet, View } from "react-native";
import { useTheme, useThemeState } from "../ThemeProvider";
import { space } from "../tokens";
import { jumpToLatestColors } from "./chatChrome";
import { JUMP_TO_LATEST_LABEL, type JumpToLatestProps } from "./JumpToLatest.types";

export type { JumpToLatestProps } from "./JumpToLatest.types";

// Liquid Glass arrived with iOS 26; SwiftUI draws the glass styles as a bare
// label before it, which would leave the label floating over the reply.
const liquidGlass = Platform.OS === "ios" && parseInt(String(Platform.Version), 10) >= 26;

/**
 * The jump back to the newest message: a small Liquid Glass circle holding only
 * the arrow, centred just above the composer. Before iOS 26 it is the same
 * circle drawn opaque in the elevated surface, so it stays legible over the
 * text it covers. Android's is ./JumpToLatest.android, Material's small FAB.
 */
export function JumpToLatest({ onPress }: JumpToLatestProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const colors = jumpToLatestColors(t);
	return (
		<View pointerEvents="box-none" style={styles.anchor}>
			<Host matchContents colorScheme={scheme}>
				<Button
					onPress={onPress}
					modifiers={[
						buttonStyle(liquidGlass ? "glass" : "borderedProminent"),
						buttonBorderShape("circle"),
						controlSize("small"),
						// A prominent button fills with its tint; glass takes it as ink.
						tint(liquidGlass ? colors.foregroundColor : colors.backgroundColor),
						accessibilityLabel(JUMP_TO_LATEST_LABEL),
					]}
				>
					<Image systemName="arrow.down" color={colors.foregroundColor} modifiers={[font({ textStyle: "footnote", weight: "semibold" })]} />
				</Button>
			</Host>
		</View>
	);
}

const styles = StyleSheet.create({
	// Centred along the bottom of the space it is given: the list above the composer.
	anchor: { position: "absolute", left: 0, right: 0, bottom: space.sm, alignItems: "center" },
});
