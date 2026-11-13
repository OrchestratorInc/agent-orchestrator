import { Host } from "@expo/ui";
import { Picker, Text } from "@expo/ui/swift-ui";
import { accessibilityIdentifier, pickerStyle, tag } from "@expo/ui/swift-ui/modifiers";
import { useThemeState } from "../ThemeProvider";
import type { SegmentedChoiceProps } from "./SegmentedChoice.types";

export type { SegmentedChoiceOption, SegmentedChoiceProps } from "./SegmentedChoice.types";

/**
 * One choice among a few views of the same content, as UIKit draws it: a
 * segmented picker with the system's sliding thumb, Dynamic Type and
 * VoiceOver's "1 of 3, selected". Android's is ./SegmentedChoice.android.
 */
export function SegmentedChoice<K extends string>({ value, options, onChange, accessibilityLabel, style, testID }: SegmentedChoiceProps<K>) {
	const { scheme } = useThemeState();
	const selected = Math.max(0, options.findIndex((option) => option.key === value));
	return (
		<Host matchContents={{ vertical: true }} colorScheme={scheme} style={[{ alignSelf: "stretch" }, style]}>
			<Picker
				label={accessibilityLabel}
				selection={selected}
				onSelectionChange={(index: number) => {
					const next = options[index];
					if (next && next.key !== value) onChange(next.key);
				}}
				modifiers={[pickerStyle("segmented"), ...(testID ? [accessibilityIdentifier(testID)] : [])]}
			>
				{options.map((option, index) => (
					<Text key={option.key} modifiers={[tag(index)]}>{option.label}</Text>
				))}
			</Picker>
		</Host>
	);
}
