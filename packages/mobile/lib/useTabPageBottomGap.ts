import { useCallback, useRef, useState, type RefObject } from "react";
import { Platform, type View } from "react-native";
import { useSafeAreaFrame } from "react-native-safe-area-context";

export type TabPageBottomGap = {
	/** Attach to the page's root view. */
	ref: RefObject<View | null>;
	/** Attach to the same view, so the gap follows the tab bar's height. */
	onLayout: () => void;
	/** How far the window reaches below the page's bottom edge. */
	gap: number;
};

/**
 * How far the window carries on below a board tab's page, for anything placed
 * from the window's bottom edge: the keyboard's height is measured from there.
 *
 * Nothing on iOS, where the page runs under the tab bar to the bottom of the
 * window. Android ends the page at the top of the tab bar instead, and only the
 * laid-out page can tell where that is. The page and the frame of the window are
 * both read in window coordinates.
 */
export function useTabPageBottomGap(): TabPageBottomGap {
	const ref = useRef<View>(null);
	const window = useSafeAreaFrame();
	const [pageBottom, setPageBottom] = useState<number>();
	const onLayout = useCallback(() => {
		if (Platform.OS !== "android") return;
		ref.current?.measureInWindow((_x, y, _width, height) => setPageBottom(y + height));
	}, []);
	const gap = pageBottom === undefined ? 0 : Math.max(window.y + window.height - pageBottom, 0);
	return { ref, onLayout, gap };
}
