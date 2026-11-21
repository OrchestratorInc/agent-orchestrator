import { createContext, useContext, useEffect } from "react";

/**
 * The sidebar drawer's controls, for the pages and header buttons that open it.
 * Kept apart from the drawer itself (SidebarDrawer.tsx), which draws with
 * Reanimated and Gesture Handler, so a page that only opens the sidebar does
 * not pull either in.
 */
export type SidebarContextValue = {
	isOpen: boolean;
	open(): void;
	close(): void;
	toggle(): void;
	/** Lets the focused page take the left-edge swipe; returns the release. */
	claimEdgeSwipe(): () => void;
};

export const SidebarContext = createContext<SidebarContextValue | null>(null);

/** The drawer's controls, or null outside it (tests, a page rendered alone). */
export function useSidebarMaybe(): SidebarContextValue | null {
	return useContext(SidebarContext);
}

/** Lets the page on screen open the sidebar with a swipe from the left edge while it is focused. */
export function useSidebarEdgeSwipe(focused: boolean) {
	const claim = useSidebarMaybe()?.claimEdgeSwipe;
	useEffect(() => {
		if (!focused || !claim) return;
		return claim();
	}, [claim, focused]);
}
