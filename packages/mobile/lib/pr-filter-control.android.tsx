import { Host, SegmentedButton, SingleChoiceSegmentedButtonRow, Text } from "@expo/ui/jetpack-compose";
import { fillMaxWidth, testID } from "@expo/ui/jetpack-compose/modifiers";
import { StyleSheet } from "react-native";
import { haptics } from "./haptics";
import type { PRFilterControlProps } from "./pr-filter-control.types";
import { prFilterLabel, prListFilters, type PRListFilter } from "./prView";
import { useTheme, useThemeState } from "./ThemeProvider";
import { gutter, space } from "./tokens";

export type { PRFilterControlProps } from "./pr-filter-control.types";

/**
 * Open / Merged / All for the pull-request list as Material 3 segmented
 * buttons, each with its count. Compose checks the chosen segment as well as
 * filling it, so the choice does not rest on colour alone, and gives each
 * segment its 48dp target, ripple and radio semantics for TalkBack.
 *
 * It heads the list under the top app bar, where M3 places a segmented
 * filter, rather than floating over the list above the navigation bar.
 */
export function PRFilterControl({ filter, counts, onChange }: PRFilterControlProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const select = (next: PRListFilter) => {
		if (next === filter) return;
		haptics.select();
		onChange(next);
	};
	return (
		<Host matchContents={{ vertical: true }} colorScheme={scheme} style={styles.slot}>
			<SingleChoiceSegmentedButtonRow modifiers={[fillMaxWidth(), testID("pr-filter")]}>
				{prListFilters.map((item) => (
					<SegmentedButton
						key={item}
						selected={item === filter}
						onClick={() => select(item)}
						colors={{
							activeContainerColor: t.accentTint,
							activeContentColor: t.accent,
							activeBorderColor: t.borderStrong,
							inactiveContainerColor: t.bgBase,
							inactiveContentColor: t.textSecondary,
							inactiveBorderColor: t.borderStrong,
						}}
						modifiers={[testID(`pr-filter-${item}`)]}
					>
						<SegmentedButton.Label>
							<Text maxLines={1} overflow="ellipsis" style={{ typography: "labelLarge", fontFamily: "DMSans_500Medium" }}>
								{prFilterLabel(item, counts[item])}
							</Text>
						</SegmentedButton.Label>
					</SegmentedButton>
				))}
			</SingleChoiceSegmentedButtonRow>
		</Host>
	);
}

const styles = StyleSheet.create({
	slot: { marginHorizontal: gutter, marginTop: space.xs, marginBottom: space.sm },
});
