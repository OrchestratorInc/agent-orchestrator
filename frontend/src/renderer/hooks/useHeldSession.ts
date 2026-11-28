import { useDeferredValue, useEffect, useLayoutEffect, useRef, useState } from "react";
import { blurSessionWorkspace, SESSION_WORKSPACE_ID, setSessionSwitchHeld } from "./useSessionSwitchSnap";

export { SESSION_WORKSPACE_ID };
/** Longest a switch keeps the previous session before rendering the destination directly. */
export const SESSION_HOLD_LIMIT_MS = 500;

/**
 * Route state reaches React as a synchronous store update. Deferring it lets
 * the sidebar answer a switch at once while the destination renders in
 * interruptible slices, so main-thread animations keep running. The session
 * already on screen stays painted, inert, until then. Entering from another
 * route has nothing to keep, so it renders directly.
 *
 * `destinationPending` says the destination's chat history is still on its
 * first read. The session on screen stays for that too, within the same limit,
 * so the destination's first frame has its history instead of its chrome over
 * a loading chat that fills in a moment later. Past the limit it renders with
 * the chat's own loading state, as before.
 */
export function useHeldSession(liveSessionId: string | undefined, liveProjectId: string | undefined, destinationPending = false) {
	const deferredSessionId = useDeferredValue(liveSessionId);
	const deferredProjectId = useDeferredValue(liveProjectId);
	// The read landing is an urgent store update; ending the hold through the
	// deferred copy keeps the destination's render interruptible, as above.
	const deferredPending = useDeferredValue(destinationPending);
	// A background render restarts on every urgent update. While the held
	// session streams, it may never finish, so past the limit the destination
	// renders the way it did before switches were deferred.
	const [forcedSessionId, setForcedSessionId] = useState<string | undefined>(undefined);
	// Only the session already on screen is kept. Returning from another route
	// before the deferred value caught up must not bring back an older session.
	const shownSessionId = useRef<string | undefined>(undefined);
	const shownProjectId = useRef<string | undefined>(undefined);
	const rendering = Boolean(liveSessionId && deferredSessionId && deferredSessionId === shownSessionId.current);
	const awaitingHistory = Boolean(
		liveSessionId && shownSessionId.current && shownSessionId.current !== liveSessionId && (destinationPending || deferredPending),
	);
	const keepShown = (rendering || awaitingHistory) && forcedSessionId !== liveSessionId;
	const sessionId = keepShown ? (rendering ? deferredSessionId : shownSessionId.current) : liveSessionId;
	const projectId = keepShown ? (rendering ? deferredProjectId : shownProjectId.current) : liveProjectId;
	const switching = sessionId !== liveSessionId;
	useLayoutEffect(() => {
		shownSessionId.current = sessionId;
		shownProjectId.current = projectId;
	});
	useLayoutEffect(() => {
		setSessionSwitchHeld(switching);
		if (!switching) return;
		// The kept session is only a placeholder now: keystrokes and clicks
		// must not reach its composer or controls (keyboard switches leave focus
		// there). inert has no visual effect.
		const workspace = document.getElementById(SESSION_WORKSPACE_ID);
		blurSessionWorkspace(workspace);
		workspace?.setAttribute("inert", "");
		return () => workspace?.removeAttribute("inert");
	}, [switching]);
	useEffect(() => {
		if (!switching || !liveSessionId) return;
		const timer = window.setTimeout(() => setForcedSessionId(liveSessionId), SESSION_HOLD_LIMIT_MS);
		return () => {
			window.clearTimeout(timer);
			// The shown session has moved on, so a stale force cannot affect a later switch.
			setForcedSessionId(undefined);
		};
	}, [switching, liveSessionId]);
	useEffect(() => () => setSessionSwitchHeld(false), []);
	return { sessionId, projectId, switching };
}
