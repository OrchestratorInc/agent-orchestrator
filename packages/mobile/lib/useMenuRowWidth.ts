import { createContext, createElement, useCallback, useContext, useLayoutEffect, useRef, useState, type ReactNode, type RefObject } from "react";
import type { LayoutChangeEvent, View, ViewStyle } from "react-native";

/** The width a list's menu rows last measured, shared by the rows under one `MenuRowWidthScope`. */
export type MenuRowWidths = RefObject<number | undefined>;

const SharedRowWidth = createContext<MenuRowWidths | null>(null);

/**
 * A holder for a list's row width that outlives the list: keep it in a screen
 * that remounts its list (a board keyed by its filter) and pass it to the
 * list's `MenuRowWidthScope`, so the rebuilt list's rows start at the width the
 * last one measured.
 */
export function useMenuRowWidths(): MenuRowWidths {
	return useRef<number | undefined>(undefined);
}

/**
 * Shares one slot width between the menu rows under it. Every row of one list
 * sits in a slot of the same width, so a row the list mounts later starts at
 * the width its siblings found instead of measuring from nothing. Put it
 * around a list whose rows all have the same width; `widths` keeps the value
 * across a remount (see `useMenuRowWidths`).
 */
export function MenuRowWidthScope({ widths, children }: { widths?: MenuRowWidths; children?: ReactNode }) {
	const own = useMenuRowWidths();
	return createElement(SharedRowWidth.Provider, { value: widths ?? own }, children);
}

/**
 * The width a full-width row keeps inside a long-press `MenuView`.
 *
 * iOS hosts a menu's trigger in SwiftUI, which sizes the host to its content:
 * a row inside it lays out at its intrinsic width, so a long single-line title
 * pushed the row past the screen instead of truncating. Measure the slot the
 * menu sits in (a plain view around it, given `ref` and `onLayout`) and give
 * the row that width (`style`), and its text truncates where the screen ends.
 *
 * A row has to have its width on its first render. Anything measured after
 * that can arrive after the row has been drawn: `onLayout` always does, and a
 * layout effect's read did for rows a list mounts in a later render batch, so
 * those rows showed a few frames at their intrinsic width (titles running off
 * the screen) before snapping. Under a `MenuRowWidthScope` a new row starts at
 * the width the list's rows last measured. The layout effect and `onLayout`
 * still measure each slot, cover a list's very first rows, and follow resizes.
 */
export function useMenuRowWidth(): {
	ref: RefObject<View | null>;
	onLayout: (event: LayoutChangeEvent) => void;
	width?: number;
	style?: ViewStyle;
} {
	const shared = useContext(SharedRowWidth);
	const ref = useRef<View>(null);
	const [width, setWidth] = useState<number | undefined>(() => shared?.current);
	const measured = useCallback((next: number) => {
		// Only a real width seeds the rows after this one: a slot laid out at
		// zero for a moment would otherwise start every later row at zero.
		if (shared && next > 0) shared.current = next;
		setWidth(next);
	}, [shared]);
	useLayoutEffect(() => {
		const slot = ref.current?.getBoundingClientRect?.().width;
		if (slot) measured(slot);
	}, [measured]);
	const onLayout = useCallback((event: LayoutChangeEvent) => measured(event.nativeEvent.layout.width), [measured]);
	return { ref, onLayout, width, style: width == null ? undefined : { width } };
}
