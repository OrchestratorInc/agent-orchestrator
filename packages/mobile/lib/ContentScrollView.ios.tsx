import { requireNativeView, requireOptionalNativeModule } from "expo";
import type { ComponentType, ReactNode } from "react";
import { View, type StyleProp, type ViewStyle } from "react-native";
import type { ContentScrollViewProps } from "./ContentScrollView.types";

type NativeProps = {
	enabled: boolean;
	style?: StyleProp<ViewStyle>;
	children: ReactNode;
};

// `modules/content-scroll-view`. A binary built before it existed has no such
// view, and asking for it anyway would throw while rendering the list.
const NativeContentScrollView: ComponentType<NativeProps> | null = requireOptionalNativeModule("AOContentScrollView")
	? requireNativeView<NativeProps>("AOContentScrollView")
	: null;

/**
 * Names the list it wraps as the scroll view the screen's navigation bar
 * observes. iOS 26 blurs content under the clear glass bar only on that view,
 * and picks it itself when the page appears: a list mounted after that, as the
 * conversation's is once it loads, was blurred under the status bar alone and
 * ran sharp under the title. Without the native view it is a plain view.
 */
export function ContentScrollView({ enabled, style, children }: ContentScrollViewProps) {
	if (!NativeContentScrollView) return <View style={style}>{children}</View>;
	return (
		<NativeContentScrollView enabled={enabled} style={style}>
			{children}
		</NativeContentScrollView>
	);
}
