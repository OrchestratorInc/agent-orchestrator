import { Host } from "@expo/ui";
import { Button, HStack, Image, Text } from "@expo/ui/swift-ui";
import { accessibilityHint, accessibilityLabel, buttonBorderShape, buttonStyle, controlSize, font, foregroundStyle, tint } from "@expo/ui/swift-ui/modifiers";
import { Platform, StyleSheet, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useTheme, useThemeState } from "../ThemeProvider";
import { iconSize, space } from "../tokens";
import { fileIconSymbols } from "./fileIcons.types";
import { reviewButtonLabel, type ReviewButtonProps } from "./ReviewButton.types";

export { REVIEW_BUTTON_CLEARANCE, type ReviewButtonProps } from "./ReviewButton.types";

/** iOS 26 draws a prominent button in Liquid Glass; earlier versions fill it. */
const glass = parseInt(String(Platform.Version), 10) >= 26;

/**
 * The queued review, floating over the Files screens until it is sent: a
 * prominent capsule, in glass from iOS 26, that opens the review sheet. Its
 * fill is the accent and its ink the accent's own, which the system's white
 * would vanish against on a near-white accent. Android's is
 * ./ReviewButton.android.
 */
export function ReviewButton({ count, onPress, bottomOffset = 0 }: ReviewButtonProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const insets = useSafeAreaInsets();
	if (!count) return null;
	const label = reviewButtonLabel(count);
	return (
		<View pointerEvents="box-none" style={[styles.dock, { bottom: Math.max(insets.bottom, space.md) + bottomOffset }]}>
			<Host matchContents colorScheme={scheme}>
				<Button
					onPress={onPress}
					modifiers={[
						buttonStyle(glass ? "glassProminent" : "borderedProminent"),
						buttonBorderShape("capsule"),
						controlSize("large"),
						tint(t.accent),
						foregroundStyle(t.onAccent),
						accessibilityLabel(label.accessibilityLabel),
						accessibilityHint("Opens the comments to send or remove"),
					]}
				>
					<HStack spacing={space.sm}>
						<Image systemName={fileIconSymbols.review.ios} size={iconSize.md} color={t.onAccent} />
						<Text modifiers={[font({ textStyle: "subheadline", weight: "semibold" })]}>{label.title}</Text>
					</HStack>
				</Button>
			</Host>
		</View>
	);
}

const styles = StyleSheet.create({
	dock: { position: "absolute", left: 0, right: 0, alignItems: "center" },
});
