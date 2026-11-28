import { useCallback, useEffect, useRef } from "react";
import { Platform } from "react-native";

/**
 * How long iOS takes to slide a dismissed form sheet off screen, with room to
 * spare: the New project sheet measured about 300 ms in the simulator.
 */
export const SHEET_DISMISS_MS = 400;

/**
 * Leaving a form sheet for a page: the sheet closes first, and the page opens
 * once it has gone.
 *
 * A page pushed while the sheet was still sliding away landed with its content
 * laid out about a screen too low (the New cloud project's page showed only
 * its title and a "0 Workers" strip at the bottom, for good). Nothing tells
 * the sheet when its dismissal ends: its route leaves the navigation state as
 * the slide starts, and a closed sheet hears no transitionEnd. So the page
 * opens once this screen unmounts, which is when its route has left the stack,
 * plus the length of the slide.
 *
 * Android has no sheet there (the same route is a page), so it opens at once.
 * `leave` closes the sheet; a second call while one is pending is ignored.
 */
export function useLeaveSheetThen(leave: () => void): (open: () => void) => void {
	const pending = useRef<(() => void) | null>(null);
	useEffect(() => () => {
		const open = pending.current;
		pending.current = null;
		if (open) setTimeout(open, SHEET_DISMISS_MS);
	}, []);
	return useCallback((open: () => void) => {
		if (Platform.OS !== "ios") {
			leave();
			open();
			return;
		}
		if (pending.current) return;
		pending.current = open;
		leave();
	}, [leave]);
}
