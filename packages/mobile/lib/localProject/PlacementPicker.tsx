import { Picker, Text } from "@expo/ui/swift-ui";
import { accessibilityIdentifier, disabled, listRowBackground, listRowInsets, pickerStyle, tag } from "@expo/ui/swift-ui/modifiers";
import { SettingsSection } from "../SettingsForm";
import { PLACEMENT_OPTIONS, type PlacementChoice } from "./LocalProjectForm.types";

/** Local and Cloud, on the page rather than in a card, as the cloud form's source. */
export function PlacementPicker({ placement }: { placement: PlacementChoice }) {
	return (
		<SettingsSection>
			<Picker
				label="Where"
				selection={placement.value}
				onSelectionChange={(next: string) => {
					const option = PLACEMENT_OPTIONS.find((item) => item.key === next);
					if (option && option.key !== placement.value) placement.onChange(option.key);
				}}
				modifiers={[pickerStyle("segmented"), listRowBackground("transparent"), listRowInsets({ top: 0, leading: 0, bottom: 0, trailing: 0 }), disabled(Boolean(placement.disabled)), accessibilityIdentifier("new-project-placement")]}
			>
				{PLACEMENT_OPTIONS.map((option) => <Text key={option.key} modifiers={[tag(option.key)]}>{option.label}</Text>)}
			</Picker>
		</SettingsSection>
	);
}
