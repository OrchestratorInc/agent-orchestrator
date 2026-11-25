import { createContext, useContext } from "react";

/**
 * Whether the app's main view (the routed page beside the sidebar) is showing.
 * Global Settings hides that view with CSS but keeps it mounted, so anything
 * that plays sound or moves on its own, such as a video, reads this to stop
 * while it is covered. Outside the main view it is always true.
 */
export const MainViewVisibleContext = createContext(true);

export function useMainViewVisible(): boolean {
	return useContext(MainViewVisibleContext);
}
