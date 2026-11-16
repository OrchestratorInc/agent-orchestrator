import { requireNativeView, requireOptionalNativeModule } from "expo";
import type { ComponentType, ReactNode } from "react";
import type { NativeSyntheticEvent, StyleProp, ViewStyle } from "react-native";
import type { HardwareReturnModifiers } from "./composerDeliveryModel";
import type { HardwareReturnProps } from "./HardwareReturn.types";

type NativeProps = {
	enabled: boolean;
	onReturnKey: (event: NativeSyntheticEvent<HardwareReturnModifiers>) => void;
	style?: StyleProp<ViewStyle>;
	children: ReactNode;
};

// `modules/hardware-return`. A binary built before it existed has no such view,
// and asking for it anyway would throw while rendering the composer.
const NativeHardwareReturn: ComponentType<NativeProps> | null = requireOptionalNativeModule("AOHardwareReturn")
	? requireNativeView<NativeProps>("AOHardwareReturn")
	: null;

/**
 * Makes Return on an iPad's hardware keyboard send, as it does in the desktop
 * composer. The native view registers key commands, which only a hardware
 * keyboard raises, so the on-screen keyboard's Return still starts a new line,
 * and so does Shift+Return.
 */
export function HardwareReturn({ enabled, onReturn, style, children }: HardwareReturnProps) {
	if (!NativeHardwareReturn) return <>{children}</>;
	return (
		<NativeHardwareReturn enabled={enabled} onReturnKey={(event) => onReturn(event.nativeEvent)} style={style}>
			{children}
		</NativeHardwareReturn>
	);
}
