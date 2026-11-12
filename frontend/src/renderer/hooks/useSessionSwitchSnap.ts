import { useRouter } from "@tanstack/react-router";
import { useEffect } from "react";

// Local, scratchpad, and remote session routes.
const SESSION_PATH = /\/sessions?\/[^/]+\/?$/;
export const SESSION_SWITCHING_ATTR = "data-session-switching";
// Upper bound when a navigation never reports rendered (blocked or superseded).
const SNAP_TIMEOUT_MS = 1000;

/** The workspace element of the session on screen; a held switch makes it inert. */
export const SESSION_WORKSPACE_ID = "session-workspace";

/** Moves focus out of the session on screen, so keystrokes typed while a switch
 *  is under way never reach its composer. */
export function blurSessionWorkspace(workspace = document.getElementById(SESSION_WORKSPACE_ID)) {
	const active = document.activeElement;
	if (workspace && active instanceof HTMLElement && workspace.contains(active)) active.blur();
}

let switchHeld = false;
let resumeAfterHold: (() => void) | undefined;

/** Keeps transitions off while the destination session is still rendering in the background. */
export function setSessionSwitchHeld(held: boolean) {
	switchHeld = held;
	if (!held) resumeAfterHold?.();
}

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
			resumeAfterHold = undefined;
			window.cancelAnimationFrame(frame);
			window.clearTimeout(timer);
		};
		const clear = () => {
			cancel();
			target = undefined;
			root.removeAttribute(SESSION_SWITCHING_ATTR);
		};
		const clearAfterTwoFrames = () => {
			resumeAfterHold = undefined;
			window.cancelAnimationFrame(frame);
			frame = window.requestAnimationFrame(() => {
				frame = window.requestAnimationFrame(() => {
					if (switchHeld) resumeAfterHold = clearAfterTwoFrames;
					else clear();
				});
			});
		};
		const offBeforeNavigate = router.subscribe("onBeforeNavigate", (event) => {
			if (!event.pathChanged) return;
			// A newer navigation away from sessions supersedes a pending switch.
			if (!SESSION_PATH.test(event.toLocation.pathname)) {
				if (target !== undefined) clear();
				return;
			}
			cancel();
			// The router renders the destination after it resolves the route, so
			// keystrokes typed in between would still reach the old composer.
			blurSessionWorkspace();
			target = event.toLocation.href;
			root.setAttribute(SESSION_SWITCHING_ATTR, "");
			timer = window.setTimeout(clear, SNAP_TIMEOUT_MS);
		});
		const offRendered = router.subscribe("onRendered", (event) => {
			if (target === undefined || event.toLocation.href !== target) return;
			clearAfterTwoFrames();
		});
		return () => {
			offBeforeNavigate();
			offRendered();
			clear();
		};
	}, [router]);
}
