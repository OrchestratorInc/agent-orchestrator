import { Host, Icon, SmallFloatingActionButton } from "@expo/ui/jetpack-compose";
import { paddingAll } from "@expo/ui/jetpack-compose/modifiers";
import { StyleSheet, View } from "react-native";
import { useTheme, useThemeState } from "../ThemeProvider";
import { iconSize, space } from "../tokens";
import { materialDrawable } from "../toolbarIcon.android";
import { jumpToLatestColors } from "./chatChrome";
import { JUMP_TO_LATEST_LABEL, type JumpToLatestProps } from "./JumpToLatest.types";

export type { JumpToLatestProps } from "./JumpToLatest.types";

// Room inside the host for the FAB's elevation shadow, which a host sized to
// the button alone would cut off at its edges.
const SHADOW_ROOM = space.sm;

/**
 * The jump back to the newest message as Material 3's small floating action
 * button: Compose's ripple, elevation, 48dp target and button semantics, in the
 * elevated surface rather than the accent, since it is not the screen's main action.
 * Centred just above the composer, as on iOS.
 */
export function JumpToLatest({ onPress }: JumpToLatestProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const colors = jumpToLatestColors(t);
	return (
		<View pointerEvents="box-none" style={styles.anchor}>
			<Host matchContents colorScheme={scheme}>
				<SmallFloatingActionButton containerColor={colors.backgroundColor} onClick={onPress} modifiers={[paddingAll(SHADOW_ROOM)]}>
					<SmallFloatingActionButton.Icon>
						<Icon source={materialDrawable("arrow_downward")} size={iconSize.xl} tint={colors.foregroundColor} contentDescription={JUMP_TO_LATEST_LABEL} />
					</SmallFloatingActionButton.Icon>
				</SmallFloatingActionButton>
			</Host>
		</View>
	);
}

const styles = StyleSheet.create({
	anchor: { position: "absolute", left: 0, right: 0, bottom: space.sm - SHADOW_ROOM, alignItems: "center" },
});
