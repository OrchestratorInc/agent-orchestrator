import { View } from "react-native";
import type { ContentScrollViewProps } from "./ContentScrollView.types";

/**
 * iOS only (see `ContentScrollView.ios.tsx`). Elsewhere a plain view, which
 * Fabric flattens away when it carries only layout.
 */
export function ContentScrollView({ style, children }: ContentScrollViewProps) {
	return <View style={style}>{children}</View>;
}
