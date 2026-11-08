import type { ReactNode } from "react";
import type { StyleProp, ViewStyle } from "react-native";

export type ContentScrollViewProps = {
	/** Whether the wrapped list is named as the one the screen's bar observes. */
	enabled: boolean;
	style?: StyleProp<ViewStyle>;
	/** A single scrollable list. */
	children: ReactNode;
};
