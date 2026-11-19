import { View } from "react-native";
import { SegmentedChoice } from "../files/SegmentedChoice";
import { PLACEMENT_OPTIONS, type PlacementChoice } from "./LocalProjectForm.types";

/** Local and Cloud, as Material segmented buttons over the form. */
export function PlacementPicker({ placement }: { placement: PlacementChoice }) {
	return (
		// Held still while a create runs: no touches, and dimmed as Material dims a disabled control.
		<View pointerEvents={placement.disabled ? "none" : "auto"} style={placement.disabled ? { opacity: 0.38 } : undefined}>
			<SegmentedChoice
				value={placement.value}
				options={PLACEMENT_OPTIONS}
				onChange={(next) => { if (next !== placement.value) placement.onChange(next); }}
				accessibilityLabel="Where the project lives"
				testID="new-project-placement"
			/>
		</View>
	);
}
