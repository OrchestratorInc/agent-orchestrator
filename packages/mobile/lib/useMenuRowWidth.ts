import { useCallback, useLayoutEffect, useRef, useState, type RefObject } from "react";
import type { LayoutChangeEvent, View, ViewStyle } from "react-native";

/**
 * The width a full-width row keeps inside a long-press `MenuView`.
 *
 * iOS hosts a menu's trigger in SwiftUI, which sizes the host to its content:
 * a row inside it lays out at its intrinsic width, so a long single-line title
 * pushed the row past the screen instead of truncating. Measure the slot the
 * menu sits in (a plain view around it, given `ref` and `onLayout`) and give
 * the row that width (`style`), and its text truncates where the screen ends.
 *
 * The slot is read in a layout effect as well as from `onLayout`. `onLayout`
 * arrives after the row has been drawn, so a new row showed one frame at its
 * intrinsic width (titles running off the screen) before snapping to the slot;
 * a board remounted by a machine filter did that for every row at once. A
 * layout effect reads the slot from the committed layout, and its update
 * renders before that frame is shown. `onLayout` still follows later changes.
 */
export function useMenuRowWidth(): {
	ref: RefObject<View | null>;
	onLayout: (event: LayoutChangeEvent) => void;
	width?: number;
	style?: ViewStyle;
} {
	const ref = useRef<View>(null);
	const [width, setWidth] = useState<number>();
	useLayoutEffect(() => {
		const measured = ref.current?.getBoundingClientRect?.().width;
		if (measured) setWidth(measured);
	}, []);
	const onLayout = useCallback((event: LayoutChangeEvent) => setWidth(event.nativeEvent.layout.width), []);
	return { ref, onLayout, width, style: width == null ? undefined : { width } };
}
