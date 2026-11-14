import type { ReactNode } from "react";
import type { StyleProp, ViewStyle } from "react-native";
import type { HardwareReturnModifiers } from "./composerDeliveryModel";

export type HardwareReturnProps = {
	/** Whether Return is taken. Off, it stays the field's and inserts a newline. */
	enabled: boolean;
	onReturn: (modifiers: HardwareReturnModifiers) => void;
	/** Applied only where the native wrapper exists; it then takes the field's place in the row. */
	style?: StyleProp<ViewStyle>;
	children: ReactNode;
};
