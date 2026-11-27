import { createContext, createElement, useCallback, useContext, useLayoutEffect, useRef, useState, type ReactNode, type RefObject } from "react";
import { Dimensions, type LayoutChangeEvent, type View, type ViewStyle } from "react-native";

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
 * the width the list's rows last measured.
 *
 * A list's very first rows have nothing measured to start from, and an unsized
 * first render does not recover: once the measured width arrives, the row is
 * the right size but its title line keeps the width it was first laid out at,
 * so a long title still runs off the screen and pushes the row's time past the
 * edge. So a row with no width to inherit starts at `estimate`, by default the
 * window's. That is only a guess where the list is narrower than the window,
 * but any width on the first render is enough: the layout effect and
 * `onLayout` then correct it, and follow resizes, without leaving the title
 * behind. A caller that sizes something else from `width`, and knows its
 * list's insets, passes a closer estimate.
 */
export function useMenuRowWidth(estimate?: number): {
	ref: RefObject<View | null>;
	onLayout: (event: LayoutChangeEvent) => void;
	width?: number;
	style?: ViewStyle;
} {
	const shared = useContext(SharedRowWidth);
	const ref = useRef<View>(null);
	const [width, setWidth] = useState<number | undefined>(() => shared?.current ?? (estimate != null && estimate > 0 ? estimate : windowWidth()));
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

/** The window's width, or undefined before the window has one. */
function windowWidth(): number | undefined {
	const width = Dimensions.get("window").width;
	return width > 0 ? width : undefined;
}
