import { Host, Picker, Text } from "@expo/ui/swift-ui";
import { pickerStyle, tag } from "@expo/ui/swift-ui/modifiers";
import { StyleSheet, View } from "react-native";
import { haptics } from "./haptics";
import type { PRFilterControlProps } from "./pr-filter-control.types";
import { prFilterLabel, prListFilters, type PRListFilter } from "./prView";
import { useThemeState } from "./ThemeProvider";
import { gutter, space } from "./tokens";

export type { PRFilterControlProps } from "./pr-filter-control.types";

/**
 * Open / Merged / All for the pull-request list, each with its count, as the
 * system's segmented control: a SwiftUI segmented Picker here, Material 3
 * segmented buttons in ./pr-filter-control.android.
 *
 * It heads the list, under the large title, the way a segmented filter sits
 * on iOS, rather than floating over the list just above the tab bar.
 */
export function PRFilterControl({ filter, counts, onChange }: PRFilterControlProps) {
	const { scheme } = useThemeState();
	return (
		<View style={styles.slot}>
			<Host matchContents={{ vertical: true }} colorScheme={scheme}>
				<Picker<PRListFilter>
					label="Pull requests"
					selection={filter}
					onSelectionChange={(next) => {
						if (next === filter) return;
						haptics.select();
						onChange(next);
					}}
					modifiers={[pickerStyle("segmented")]}
					testID="pr-filter"
				>
					{prListFilters.map((item) => (
						<Text key={item} modifiers={[tag(item)]}>
							{prFilterLabel(item, counts[item])}
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
