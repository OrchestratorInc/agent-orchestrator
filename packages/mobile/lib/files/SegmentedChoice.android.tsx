import { Host, SegmentedButton, SingleChoiceSegmentedButtonRow, Text } from "@expo/ui/jetpack-compose";
import { fillMaxWidth, testID as testTag } from "@expo/ui/jetpack-compose/modifiers";
import { useTheme, useThemeState } from "../ThemeProvider";
import type { SegmentedChoiceProps } from "./SegmentedChoice.types";

export type { SegmentedChoiceOption, SegmentedChoiceProps } from "./SegmentedChoice.types";

/**
 * Material 3's single-choice segmented buttons: the checked segment filled,
 * the 48dp row, the ripple, and TalkBack's radio-button semantics, all from
 * Compose. The fill is the accent tint the app's other chosen chips use.
 */
export function SegmentedChoice<K extends string>({ value, options, onChange, style, testID }: SegmentedChoiceProps<K>) {
	const t = useTheme();
	const { scheme } = useThemeState();
	return (
		<Host matchContents={{ vertical: true }} colorScheme={scheme} style={[{ alignSelf: "stretch" }, style]}>
			<SingleChoiceSegmentedButtonRow modifiers={[fillMaxWidth(), ...(testID ? [testTag(testID)] : [])]}>
				{options.map((option) => (
					<SegmentedButton
						key={option.key}
						selected={option.key === value}
						onClick={() => { if (option.key !== value) onChange(option.key); }}
						colors={{
							activeContainerColor: t.accentTint,
							activeContentColor: t.textPrimary,
							activeBorderColor: t.borderDefault,
							inactiveContainerColor: "transparent",
							inactiveContentColor: t.textSecondary,
							inactiveBorderColor: t.borderDefault,
						}}
					>
						<SegmentedButton.Label>
							<Text maxLines={1} overflow="ellipsis" style={{ typography: "labelLarge", fontFamily: "DMSans_500Medium" }}>
								{option.label}
							</Text>
						</SegmentedButton.Label>
					</SegmentedButton>
				))}
			</SingleChoiceSegmentedButtonRow>
		</Host>
	);
}
