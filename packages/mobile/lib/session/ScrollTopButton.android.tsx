import { Host, Icon, SmallFloatingActionButton } from "@expo/ui/jetpack-compose";
import { paddingAll } from "@expo/ui/jetpack-compose/modifiers";
import { StyleSheet } from "react-native";
import { useTheme, useThemeState } from "../ThemeProvider";
import { materialDrawable } from "../toolbarIcon.android";
import { iconSize, space } from "../tokens";
import { SCROLL_TOP_GLYPH, SCROLL_TOP_LABEL, type ScrollTopButtonProps } from "./ScrollTopButton.types";

export type { ScrollTopButtonProps } from "./ScrollTopButton.types";

const MARGIN = space.lg;
// Room inside the host for the FAB's elevation shadow, which a host sized to
// the button alone would cut off at its edges.
const SHADOW_ROOM = space.sm;

/**
 * Material 3's small FAB, 16dp in from the grid's bottom-end corner: a
 * secondary floating action, on the elevated surface rather than the accent
 * the screen's primary actions use. Compose gives it the 48dp touch target,
 * the ripple and the button semantics.
 */
export function ScrollTopButton({ onPress }: ScrollTopButtonProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	return (
		<Host matchContents colorScheme={scheme} style={styles.anchor}>
			<SmallFloatingActionButton containerColor={t.bgElevated} onClick={onPress} modifiers={[paddingAll(SHADOW_ROOM)]}>
				<SmallFloatingActionButton.Icon>
					<Icon source={materialDrawable(SCROLL_TOP_GLYPH.android)} size={iconSize.xl} tint={t.textPrimary} contentDescription={SCROLL_TOP_LABEL} />
				</SmallFloatingActionButton.Icon>
			</SmallFloatingActionButton>
		</Host>
	);
}

const styles = StyleSheet.create({
	anchor: { position: "absolute", end: MARGIN - SHADOW_ROOM, bottom: MARGIN - SHADOW_ROOM },
});
