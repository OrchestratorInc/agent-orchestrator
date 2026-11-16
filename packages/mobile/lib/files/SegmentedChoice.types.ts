import type { StyleProp, ViewStyle } from "react-native";

export type SegmentedChoiceOption<K extends string> = {
	key: K;
	/** The segment's text, count included: "Changes 12". */
	label: string;
};

export type SegmentedChoiceProps<K extends string> = {
	value: K;
	options: readonly SegmentedChoiceOption<K>[];
	onChange: (key: K) => void;
	/** What the choice is between, for screen readers: "View". */
	accessibilityLabel: string;
	style?: StyleProp<ViewStyle>;
	testID?: string;
};
