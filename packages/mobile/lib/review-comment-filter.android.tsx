import { Host, SegmentedButton, SingleChoiceSegmentedButtonRow, Text } from "@expo/ui/jetpack-compose";
import { fillMaxWidth, testID } from "@expo/ui/jetpack-compose/modifiers";
import { StyleSheet } from "react-native";
import { haptics } from "./haptics";
import { reviewCommentViewLabel, reviewCommentViews, type ReviewCommentFilterProps, type ReviewCommentView } from "./review-comment-filter.types";
import { useTheme, useThemeState } from "./ThemeProvider";
import { gutter, space } from "./tokens";

export type { ReviewCommentFilterProps } from "./review-comment-filter.types";

/**
 * Open / Resolved for a PR's review comments as Material 3 segmented buttons,
 * each with its count, drawn as the PR list's filter is.
 */
export function ReviewCommentFilter({ view, counts, onChange }: ReviewCommentFilterProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const select = (next: ReviewCommentView) => {
		if (next === view) return;
		haptics.select();
		onChange(next);
	};
	return (
		<Host matchContents={{ vertical: true }} colorScheme={scheme} style={styles.slot}>
			<SingleChoiceSegmentedButtonRow modifiers={[fillMaxWidth(), testID("review-comment-filter")]}>
				{reviewCommentViews.map((item) => (
					<SegmentedButton
						key={item}
						selected={item === view}
						onClick={() => select(item)}
						colors={{
							activeContainerColor: t.accentTint,
							activeContentColor: t.accent,
							activeBorderColor: t.borderStrong,
							inactiveContainerColor: t.bgBase,
							inactiveContentColor: t.textSecondary,
							inactiveBorderColor: t.borderStrong,
						}}
						modifiers={[testID(`review-comment-filter-${item}`)]}
					>
						<SegmentedButton.Label>
							<Text maxLines={1} overflow="ellipsis" style={{ typography: "labelLarge", fontFamily: "DMSans_500Medium" }}>
								{reviewCommentViewLabel(item, counts[item])}
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
