import { useRouter } from "@tanstack/react-router";
import { useEffect } from "react";

// Local, scratchpad, and remote session routes.
const SESSION_PATH = /\/sessions?\/[^/]+\/?$/;
export const SESSION_SWITCHING_ATTR = "data-session-switching";
// Upper bound when a navigation never reports rendered (blocked or superseded).
const SNAP_TIMEOUT_MS = 1000;

/**
 * A session switch paints the destination's chrome (topbar reserve, compact
 * labels) in its first frame. Those CSS transitions are for changes inside a
 * session, so suppress them from navigation start until two frames after the
 * route renders: frame one paints the final geometry with transitions off,
 * frame two re-enables them with nothing left to interpolate. Set before React
 * renders so no forced layout in a commit can start a transition early.
 */
export function useSessionSwitchSnap() {
	const router = useRouter();
	useEffect(() => {
		const root = document.documentElement;
		let frame = 0;
		let timer = 0;
		// The navigation the flag belongs to. During rapid switching, only its own
		// render may clear the flag, not an earlier one that lands late.
		let target: string | undefined;
		const cancel = () => {
			window.cancelAnimationFrame(frame);
			window.clearTimeout(timer);
		};
		const clear = () => {
			cancel();
			target = undefined;
			root.removeAttribute(SESSION_SWITCHING_ATTR);
		};
		const offBeforeNavigate = router.subscribe("onBeforeNavigate", (event) => {
			if (!event.pathChanged) return;
			// A newer navigation away from sessions supersedes a pending switch.
			if (!SESSION_PATH.test(event.toLocation.pathname)) {
				if (target !== undefined) clear();
				return;
			}
			cancel();
			target = event.toLocation.href;
			root.setAttribute(SESSION_SWITCHING_ATTR, "");
			timer = window.setTimeout(clear, SNAP_TIMEOUT_MS);
		});
		const offRendered = router.subscribe("onRendered", (event) => {
			if (target === undefined || event.toLocation.href !== target) return;
			window.cancelAnimationFrame(frame);
			frame = window.requestAnimationFrame(() => {
				frame = window.requestAnimationFrame(clear);
			});
		});
		return () => {
			offBeforeNavigate();
			offRendered();
			clear();
		};
	}, [router]);
}
