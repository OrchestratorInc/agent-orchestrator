import { useCallback, useState } from "react";
import type { LayoutChangeEvent, ViewStyle } from "react-native";

/**
 * The width a full-width row keeps inside a long-press `MenuView`.
 *
 * iOS hosts a menu's trigger in SwiftUI, which sizes the host to its content:
 * a row inside it lays out at its intrinsic width, so a long single-line title
 * pushed the row past the screen instead of truncating. Measure the slot the
 * menu sits in (`onLayout` on a plain view around it) and give the row that
 * width (`style`), and its text truncates where the screen ends.
 */
export function useMenuRowWidth(): { onLayout: (event: LayoutChangeEvent) => void; width?: number; style?: ViewStyle } {
	const [width, setWidth] = useState<number>();
	const onLayout = useCallback((event: LayoutChangeEvent) => setWidth(event.nativeEvent.layout.width), []);
	return { onLayout, width, style: width == null ? undefined : { width } };
}
