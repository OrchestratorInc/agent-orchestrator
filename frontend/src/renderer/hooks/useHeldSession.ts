import { useDeferredValue, useEffect, useLayoutEffect, useRef, useState } from "react";
import { blurSessionWorkspace, SESSION_WORKSPACE_ID, setSessionSwitchHeld } from "./useSessionSwitchSnap";

export { SESSION_WORKSPACE_ID };
export const SESSION_HOLD_LIMIT_MS = 500;

export function useHeldSession(liveSessionId: string | undefined, liveProjectId: string | undefined) {
	const deferredSessionId = useDeferredValue(liveSessionId);
	const deferredProjectId = useDeferredValue(liveProjectId);
	const [forcedSessionId, setForcedSessionId] = useState<string | undefined>(undefined);
	const shownSessionId = useRef<string | undefined>(undefined);
	const keepShown = Boolean(
		liveSessionId && deferredSessionId && deferredSessionId === shownSessionId.current && forcedSessionId !== liveSessionId,
	);
	const sessionId = keepShown ? deferredSessionId : liveSessionId;
	const projectId = keepShown ? deferredProjectId : liveProjectId;
	const switching = sessionId !== liveSessionId;
	useLayoutEffect(() => {
		shownSessionId.current = sessionId;
	});
	useLayoutEffect(() => {
		setSessionSwitchHeld(switching);
		if (!switching) return;
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
			setForcedSessionId(undefined);
		};
	}, [switching, liveSessionId]);
	useEffect(() => () => setSessionSwitchHeld(false), []);
	return { sessionId, projectId, switching };
}
