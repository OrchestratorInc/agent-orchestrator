import { useEffect, useRef } from "react";
import { Alert } from "react-native";
import { INTERFACE_SWITCH_CONFIRM as copy, type InterfaceSwitchConfirmProps } from "./InterfaceSwitchConfirm.types";

export type { InterfaceSwitchConfirmProps, InterfaceSwitchPolicy } from "./InterfaceSwitchConfirm.types";

/**
 * Android asks with the system dialog: three buttons is exactly what it holds.
 * Back and a tap outside keep the terminal, like the cancel button.
 */
export function InterfaceSwitchConfirm({ message, onChoose, onCancel }: InterfaceSwitchConfirmProps): null {
	const handlers = useRef({ onChoose, onCancel });
	handlers.current = { onChoose, onCancel };

	useEffect(() => {
		if (message === null) return;
		Alert.alert(
			copy.title,
			message,
			[
				{ text: copy.cancel, style: "cancel", onPress: () => handlers.current.onCancel() },
				{ text: copy.drain, onPress: () => handlers.current.onChoose("drain") },
				{ text: copy.interrupt, style: "destructive", onPress: () => handlers.current.onChoose("interrupt") },
			],
			{ cancelable: true, onDismiss: () => handlers.current.onCancel() },
		);
	}, [message]);

	return null;
}
