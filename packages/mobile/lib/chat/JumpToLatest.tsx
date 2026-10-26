import { Host } from "@expo/ui";
import { Button, HStack, Image, Text } from "@expo/ui/swift-ui";
import { accessibilityLabel, buttonBorderShape, buttonStyle, controlSize, font, foregroundStyle, tint } from "@expo/ui/swift-ui/modifiers";
import { Platform, StyleSheet } from "react-native";
import { useTheme, useThemeState } from "../ThemeProvider";
import { iconSize, space, type } from "../tokens";
import { jumpToLatestColors } from "./chatChrome";
import { JUMP_TO_LATEST_LABEL, type JumpToLatestProps } from "./JumpToLatest.types";

export type { JumpToLatestProps } from "./JumpToLatest.types";

// Liquid Glass arrived with iOS 26; SwiftUI draws the glass styles as a bare
// label before it, which would leave the label floating over the reply.
const liquidGlass = Platform.OS === "ios" && parseInt(String(Platform.Version), 10) >= 26;

/**
 * The jump back to the newest message, floating over the conversation: a Liquid
 * Glass capsule on iOS. Before iOS 26 it is the same capsule drawn opaque in the
 * elevated surface, so it stays legible over the text it covers.
 * Android's is ./JumpToLatest.android, Material's small FAB.
 */
export function JumpToLatest({ onPress }: JumpToLatestProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const colors = jumpToLatestColors(t);
	return (
		<Host matchContents colorScheme={scheme} style={styles.anchor}>
			<Button
				onPress={onPress}
				modifiers={[
					buttonStyle(liquidGlass ? "glass" : "borderedProminent"),
					buttonBorderShape("capsule"),
					controlSize("large"),
					// A prominent button fills with its tint; glass takes it as ink.
					tint(liquidGlass ? colors.foregroundColor : colors.backgroundColor),
					accessibilityLabel(JUMP_TO_LATEST_LABEL),
				]}
			>
				<HStack spacing={space.xs}>
					<Image systemName="arrow.down" size={iconSize.sm} color={colors.foregroundColor} />
					<Text modifiers={[font({ textStyle: "subheadline", family: "Geist_600SemiBold", size: type.subheadline.fontSize }), foregroundStyle(colors.foregroundColor)]}>Latest</Text>
				</HStack>
			</Button>
		</Host>
	);
}

const styles = StyleSheet.create({
	anchor: { position: "absolute", right: space.lg, bottom: space.md },
});
