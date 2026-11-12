import { Host, SegmentedButton, SingleChoiceSegmentedButtonRow, Text } from "@expo/ui/jetpack-compose";
import { testID } from "@expo/ui/jetpack-compose/modifiers";
import { StyleSheet, View } from "react-native";
import { haptics } from "./haptics";
import { useTheme, useThemeState } from "./ThemeProvider";
import { space } from "./tokens";
import { viewModeLabel, viewModes, type ViewMode, type ViewModeControlProps } from "./view-mode-control.types";

export type { ViewMode, ViewModeControlProps } from "./view-mode-control.types";

/**
 * A rendered file's view and its source as Material 3 segmented buttons. Compose
 * checks the chosen segment as well as filling it, so the choice does not rest
 * on colour alone, and gives each segment its 48dp target and radio semantics.
 */
export function ViewModeControl({ mode, renderedLabel, onChange }: ViewModeControlProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const select = (next: ViewMode) => {
		if (next === mode) return;
		haptics.select();
		onChange(next);
	};
	return (
		<View style={styles.slot}>
			<Host matchContents={{ vertical: true }} colorScheme={scheme} style={styles.host}>
				<SingleChoiceSegmentedButtonRow modifiers={[testID("view-mode")]}>
					{viewModes.map((item) => (
						<SegmentedButton
							key={item}
							selected={item === mode}
							onClick={() => select(item)}
							colors={{
								activeContainerColor: t.accentTint,
								activeContentColor: t.accent,
								activeBorderColor: t.borderStrong,
								inactiveContainerColor: t.bgBase,
								inactiveContentColor: t.textSecondary,
								inactiveBorderColor: t.borderStrong,
							}}
							modifiers={[testID(`view-mode-${item}`)]}
						>
							<SegmentedButton.Label>
								<Text maxLines={1} style={{ typography: "labelLarge", fontFamily: "DMSans_500Medium" }}>
									{viewModeLabel(item, renderedLabel)}
								</Text>
							</SegmentedButton.Label>
						</SegmentedButton>
					))}
				</SingleChoiceSegmentedButtonRow>
			</Host>
		</View>
	);
}

const styles = StyleSheet.create({
	slot: { alignItems: "center", paddingTop: space.xs, paddingBottom: space.sm },
	host: { width: 260 },
});
