import { SegmentedChoice } from "../files/SegmentedChoice";
import { PLACEMENT_OPTIONS, type PlacementChoice } from "./LocalProjectForm.types";

/** Local and Cloud, as Material segmented buttons over the form. */
export function PlacementPicker({ placement }: { placement: PlacementChoice }) {
	return (
		<SegmentedChoice
			value={placement.value}
			options={PLACEMENT_OPTIONS}
			onChange={(next) => { if (next !== placement.value) placement.onChange(next); }}
			accessibilityLabel="Where the project lives"
			testID="new-project-placement"
		/>
	);
}
