import { Host } from "@expo/ui";
import { Image, ZStack } from "@expo/ui/swift-ui";
import { accessibilityAddTraits, accessibilityElement, accessibilityLabel, frame, onTapGesture } from "@expo/ui/swift-ui/modifiers";
import { Platform, Pressable, StyleSheet, View } from "react-native";
import { AppSymbol } from "../AppSymbol";
import { rowDividerWidth } from "../divider";
import { glassCircle } from "../glass";
import { usePressFeedback } from "../pressFeedback";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles, useThemeState } from "../ThemeProvider";
import { iconSize, space, touchTarget } from "../tokens";
import { SCROLL_TOP_GLYPH, SCROLL_TOP_LABEL, type ScrollTopButtonProps } from "./ScrollTopButton.types";

export type { ScrollTopButtonProps } from "./ScrollTopButton.types";

// Liquid Glass arrived in iOS 26; earlier versions draw the glass modifier as
// nothing, so they get a plain filled circle instead.
const glassSupported = Platform.OS === "ios" && parseInt(String(Platform.Version), 10) >= 26;

/**
 * The terminal's jump back to its oldest output, floating over the bottom-end
 * corner of the grid: a Liquid Glass circle sized to the touch target, the
 * material iOS 26 gives controls that float over content. Android draws
 * Material's small FAB (./ScrollTopButton.android).
 */
export function ScrollTopButton({ onPress }: ScrollTopButtonProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const s = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("button");
	if (glassSupported) {
		return (
			<View style={s.anchor}>
				<Host style={s.circle} colorScheme={scheme}>
					{/* The gesture sits on the glass stack, as in the header's glass
					    buttons, so the whole circle takes the tap and the material
					    gives its own press response. */}
					<ZStack
						modifiers={[
							frame({ width: touchTarget, height: touchTarget }),
							glassCircle(),
							onTapGesture(() => onPress()),
							// A ZStack, not a Group: a Group hands its modifiers to its
							// child, so VoiceOver's element was the bare glyph, about
							// 15×20pt. Ignoring the children makes the framed circle the
							// element: 44pt.
							accessibilityElement("ignore"),
							accessibilityAddTraits(["isButton"]),
							accessibilityLabel(SCROLL_TOP_LABEL),
						]}
					>
						<Image systemName={SCROLL_TOP_GLYPH.ios} size={iconSize.lg} color={t.textPrimary} />
					</ZStack>
				</Host>
			</View>
		);
	}
	return (
		<View style={s.anchor}>
			<Pressable
				accessibilityRole="button"
				accessibilityLabel={SCROLL_TOP_LABEL}
				onPress={onPress}
				android_ripple={feedback.android_ripple}
				style={({ pressed }) => [s.circle, s.filled, pressed && feedback.pressed]}
			>
				<AppSymbol name={SCROLL_TOP_GLYPH} size={iconSize.lg} color={t.textPrimary} />
			</Pressable>
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		anchor: { position: "absolute", right: space.lg, bottom: space.lg },
		circle: { width: touchTarget, height: touchTarget, borderRadius: touchTarget / 2, overflow: "hidden", alignItems: "center", justifyContent: "center" },
		filled: { backgroundColor: t.bgElevated, borderWidth: rowDividerWidth, borderColor: t.borderDefault },
	});
