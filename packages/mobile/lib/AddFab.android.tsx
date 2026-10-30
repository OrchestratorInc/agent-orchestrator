import { ExtendedFloatingActionButton, Host, Icon, Text } from "@expo/ui/jetpack-compose";
import { paddingAll, testID as testTag } from "@expo/ui/jetpack-compose/modifiers";
import { StyleSheet } from "react-native";
import type { AddFabProps } from "./AddFab.types";
import { useTheme, useThemeState } from "./ThemeProvider";
import { materialDrawable } from "./toolbarIcon.android";
import { font, iconSize, space } from "./tokens";

export type { AddFabProps } from "./AddFab.types";

/** Material 3's extended FAB, set 16dp in from the bottom-end corner. */
const FAB_SIZE = 56;
const FAB_MARGIN = space.lg;
// Room inside the host for the FAB's elevation shadow, which a host sized to
// the button alone would cut off at its edges.
const SHADOW_ROOM = space.sm;

/** How much of a list's bottom the button covers, margin included. */
export const addFabClearance = FAB_SIZE + FAB_MARGIN;

/**
 * A screen's create action as Material 3's extended floating action button: the
 * add symbol and "New" on the accent fill, with Compose's ripple, elevation and
 * semantics.
 */
export function AddFab({ label, onPress, bottomInset, testID }: AddFabProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	return (
		<Host
			matchContents
			colorScheme={scheme}
			style={[styles.anchor, { bottom: bottomInset + FAB_MARGIN - SHADOW_ROOM }]}
		>
			<ExtendedFloatingActionButton
				containerColor={t.accent}
				onClick={onPress}
				expanded
				modifiers={[paddingAll(SHADOW_ROOM), ...(testID ? [testTag(testID)] : [])]}
			>
				<ExtendedFloatingActionButton.Icon>
					{/* An accent container is not one of the Material scheme's own, so
					    Compose has no content colour to pair with it. */}
					<Icon source={materialDrawable("add")} size={iconSize.xl} tint={t.onAccent} contentDescription={label} />
				</ExtendedFloatingActionButton.Icon>
				<ExtendedFloatingActionButton.Text>
					<Text color={t.onAccent} style={{ typography: "labelLarge", fontFamily: font.semibold }}>New</Text>
				</ExtendedFloatingActionButton.Text>
			</ExtendedFloatingActionButton>
		</Host>
	);
}

const styles = StyleSheet.create({
	anchor: { position: "absolute", end: FAB_MARGIN - SHADOW_ROOM },
});
