import { Host, Picker, Text } from "@expo/ui/swift-ui";
import { pickerStyle, tag } from "@expo/ui/swift-ui/modifiers";
import { StyleSheet, View } from "react-native";
import { haptics } from "./haptics";
import { useThemeState } from "./ThemeProvider";
import { space } from "./tokens";
import { viewModeLabel, viewModes, type ViewMode, type ViewModeControlProps } from "./view-mode-control.types";

export type { ViewMode, ViewModeControlProps } from "./view-mode-control.types";

/**
 * A rendered file's view and its source, as the desktop's Rendered / Source
 * switch: a SwiftUI segmented Picker here, Material 3 segmented buttons in
 * ./view-mode-control.android. It heads the file, under the header.
 */
export function ViewModeControl({ mode, renderedLabel, onChange }: ViewModeControlProps) {
	const { scheme } = useThemeState();
	return (
		<View style={styles.slot}>
			<Host matchContents={{ vertical: true }} colorScheme={scheme} style={styles.host}>
				<Picker<ViewMode>
					label="View"
					selection={mode}
					onSelectionChange={(next) => {
						if (next === mode) return;
						haptics.select();
						onChange(next);
					}}
					modifiers={[pickerStyle("segmented")]}
					testID="view-mode"
				>
					{viewModes.map((item) => (
						<Text key={item} modifiers={[tag(item)]}>
							{viewModeLabel(item, renderedLabel)}
						</Text>
					))}
				</Picker>
			</Host>
		</View>
	);
}

const styles = StyleSheet.create({
	slot: { alignItems: "center", paddingTop: space.xs, paddingBottom: space.sm },
	host: { width: 240 },
});
