import { Host, Picker, Text } from "@expo/ui/swift-ui";
import { pickerStyle, tag } from "@expo/ui/swift-ui/modifiers";
import { StyleSheet, View } from "react-native";
import { haptics } from "./haptics";
import { reviewCommentViewLabel, reviewCommentViews, type ReviewCommentFilterProps, type ReviewCommentView } from "./review-comment-filter.types";
import { useThemeState } from "./ThemeProvider";
import { gutter, space } from "./tokens";

export type { ReviewCommentFilterProps } from "./review-comment-filter.types";

/**
 * Open / Resolved for a PR's review comments, each with its count, as the
 * system's segmented control: a SwiftUI segmented Picker here, Material 3
 * segmented buttons in ./review-comment-filter.android. Like the PR list's filter.
 */
export function ReviewCommentFilter({ view, counts, onChange }: ReviewCommentFilterProps) {
	const { scheme } = useThemeState();
	return (
		<View style={styles.slot}>
			<Host matchContents={{ vertical: true }} colorScheme={scheme}>
				<Picker<ReviewCommentView>
					label="Review comments"
					selection={view}
					onSelectionChange={(next) => {
						if (next === view) return;
						haptics.select();
						onChange(next);
					}}
					modifiers={[pickerStyle("segmented")]}
					testID="review-comment-filter"
				>
					{reviewCommentViews.map((item) => (
						<Text key={item} modifiers={[tag(item)]}>
							{reviewCommentViewLabel(item, counts[item])}
						</Text>
					))}
				</Picker>
			</Host>
		</View>
	);
}

const styles = StyleSheet.create({
	slot: { paddingHorizontal: gutter, paddingTop: space.xs, paddingBottom: space.sm },
});
