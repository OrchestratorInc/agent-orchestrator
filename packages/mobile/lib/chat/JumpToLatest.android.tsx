import { Host, Icon, SmallFloatingActionButton } from "@expo/ui/jetpack-compose";
import { paddingAll } from "@expo/ui/jetpack-compose/modifiers";
import { StyleSheet } from "react-native";
import { useTheme, useThemeState } from "../ThemeProvider";
import { iconSize, space } from "../tokens";
import { materialDrawable } from "../toolbarIcon.android";
import { jumpToLatestColors } from "./chatChrome";
import { JUMP_TO_LATEST_LABEL, type JumpToLatestProps } from "./JumpToLatest.types";

export type { JumpToLatestProps } from "./JumpToLatest.types";

/** Material 3 sets a FAB 16dp in from the bottom-end corner. */
const FAB_MARGIN = space.lg;
// Room inside the host for the FAB's elevation shadow, which a host sized to
// the button alone would cut off at its edges.
const SHADOW_ROOM = space.sm;

/**
 * The jump back to the newest message as Material 3's small floating action
 * button: Compose's ripple, elevation, 48dp target and button semantics, in the
 * elevated surface rather than the accent, since it is not the screen's main action.
 */
export function JumpToLatest({ onPress }: JumpToLatestProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const colors = jumpToLatestColors(t);
	return (
		<Host matchContents colorScheme={scheme} style={styles.anchor}>
			<SmallFloatingActionButton containerColor={colors.backgroundColor} onClick={onPress} modifiers={[paddingAll(SHADOW_ROOM)]}>
				<SmallFloatingActionButton.Icon>
					<Icon source={materialDrawable("arrow_downward")} size={iconSize.xl} tint={colors.foregroundColor} contentDescription={JUMP_TO_LATEST_LABEL} />
				</SmallFloatingActionButton.Icon>
			</SmallFloatingActionButton>
		</Host>
	);
}

const styles = StyleSheet.create({
	anchor: { position: "absolute", end: FAB_MARGIN - SHADOW_ROOM, bottom: FAB_MARGIN - SHADOW_ROOM },
});
