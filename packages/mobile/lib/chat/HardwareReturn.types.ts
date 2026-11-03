import type { ReactNode } from "react";
import type { StyleProp, ViewStyle } from "react-native";
import type { HardwareReturnModifiers } from "./composerDeliveryModel";

export type HardwareReturnProps = {
	/** Off while the field is not editable, so Return reaches nothing. */
	enabled: boolean;
	onReturn: (modifiers: HardwareReturnModifiers) => void;
	/** Applied only where the native wrapper exists; it then takes the field's place in the row. */
	style?: StyleProp<ViewStyle>;
	children: ReactNode;
};
