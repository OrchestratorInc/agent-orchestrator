import { ExtendedFloatingActionButton, Host, Icon, Text } from "@expo/ui/jetpack-compose";
import { paddingAll } from "@expo/ui/jetpack-compose/modifiers";
import { StyleSheet } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useTheme, useThemeState } from "../ThemeProvider";
import { iconSize, space } from "../tokens";
import { fileDrawable } from "./fileIcons.android";
import { reviewButtonLabel, type ReviewButtonProps } from "./ReviewButton.types";

export { REVIEW_BUTTON_CLEARANCE, type ReviewButtonProps } from "./ReviewButton.types";

/** Material 3's FAB margin from the bottom-end corner. */
const FAB_MARGIN = space.lg;
// Room inside the host for the FAB's elevation shadow, which a host sized to
// the button alone would cut off at its edges.
const SHADOW_ROOM = space.sm;

/**
 * The queued review as Material 3's extended FAB at the bottom-end corner:
 * the review glyph and "Send 3 comments" on the accent, opening the review
 * sheet. Compose gives it the ripple, the elevation and the button semantics.
 */
export function ReviewButton({ count, onPress, bottomOffset = 0 }: ReviewButtonProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const insets = useSafeAreaInsets();
	if (!count) return null;
	const label = reviewButtonLabel(count);
	return (
		<Host matchContents colorScheme={scheme} style={[styles.anchor, { bottom: insets.bottom + bottomOffset + FAB_MARGIN - SHADOW_ROOM }]}>
			<ExtendedFloatingActionButton containerColor={t.accent} onClick={onPress} modifiers={[paddingAll(SHADOW_ROOM)]}>
				<ExtendedFloatingActionButton.Icon>
					{/* An accent container is not one of the Material scheme's own, so
					    Compose has no content colour to pair with it. */}
					<Icon source={fileDrawable("review")} size={iconSize.xl} tint={t.onAccent} contentDescription={label.accessibilityLabel} />
				</ExtendedFloatingActionButton.Icon>
				<ExtendedFloatingActionButton.Text>
					<Text color={t.onAccent} style={{ typography: "labelLarge", fontFamily: "Geist_600SemiBold" }}>{label.title}</Text>
				</ExtendedFloatingActionButton.Text>
			</ExtendedFloatingActionButton>
		</Host>
	);
}

const styles = StyleSheet.create({
	anchor: { position: "absolute", end: FAB_MARGIN - SHADOW_ROOM },
});
