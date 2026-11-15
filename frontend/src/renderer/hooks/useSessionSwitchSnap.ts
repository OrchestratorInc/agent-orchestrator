import { useRouter } from "@tanstack/react-router";
import { useEffect } from "react";

const SESSION_PATH = /\/sessions?\/[^/]+\/?$/;
export const SESSION_SWITCHING_ATTR = "data-session-switching";
const SNAP_TIMEOUT_MS = 1000;

export const SESSION_WORKSPACE_ID = "session-workspace";

export function blurSessionWorkspace(workspace = document.getElementById(SESSION_WORKSPACE_ID)) {
	const active = document.activeElement;
	if (workspace && active instanceof HTMLElement && workspace.contains(active)) active.blur();
}

let switchHeld = false;
let resumeAfterHold: (() => void) | undefined;

export function setSessionSwitchHeld(held: boolean) {
	switchHeld = held;
	if (!held) resumeAfterHold?.();
}

export function useSessionSwitchSnap() {
	const router = useRouter();
	useEffect(() => {
		const root = document.documentElement;
		let frame = 0;
		let timer = 0;
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
			if (!SESSION_PATH.test(event.toLocation.pathname)) {
				if (target !== undefined) clear();
				return;
			}
			cancel();
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
