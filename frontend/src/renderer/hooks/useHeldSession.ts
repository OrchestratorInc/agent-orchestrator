import { useDeferredValue, useEffect, useLayoutEffect, useRef } from "react";
import { setSessionSwitchHeld } from "./useSessionSwitchSnap";

/** The workspace element of the session on screen; the held view's inert root. */
export const SESSION_WORKSPACE_ID = "session-workspace";

/**
 * Route state reaches React as a synchronous store update. Deferring it lets
 * the sidebar answer a switch at once while the destination renders in
 * interruptible slices, so main-thread animations keep running. The session
 * already on screen stays painted, inert, until then. Entering from another
 * route has nothing to keep, so it renders directly.
 */
export function useHeldSession(liveSessionId: string | undefined, liveProjectId: string | undefined) {
	const deferredSessionId = useDeferredValue(liveSessionId);
	const deferredProjectId = useDeferredValue(liveProjectId);
	// Only the session already on screen is kept. Returning from another route
	// before the deferred value caught up must not bring back an older session.
	const shownSessionId = useRef<string | undefined>(undefined);
	const keepShown = Boolean(liveSessionId && deferredSessionId && deferredSessionId === shownSessionId.current);
	const sessionId = keepShown ? deferredSessionId : liveSessionId;
	const projectId = keepShown ? deferredProjectId : liveProjectId;
	const switching = sessionId !== liveSessionId;
	useLayoutEffect(() => {
		shownSessionId.current = sessionId;
	});
	useLayoutEffect(() => {
		setSessionSwitchHeld(switching);
		if (!switching) return;
		// The kept session is only a placeholder now: keystrokes and clicks
		// must not reach its composer or controls (keyboard switches leave focus
		// there). inert has no visual effect.
		const workspace = document.getElementById(SESSION_WORKSPACE_ID);
		const active = document.activeElement;
		if (workspace && active instanceof HTMLElement && workspace.contains(active)) active.blur();
		workspace?.setAttribute("inert", "");
		return () => workspace?.removeAttribute("inert");
	}, [switching]);
	useEffect(() => () => setSessionSwitchHeld(false), []);
	return { sessionId, projectId, switching };
}
