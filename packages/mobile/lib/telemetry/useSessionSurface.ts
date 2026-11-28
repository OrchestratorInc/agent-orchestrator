import { useFocusEffect } from "expo-router";
import { useCallback, useEffect, useRef } from "react";
import type { Surface } from "./surfaces";
import { uiUsage } from "./uiUsageRegistry";

const VARIANT = { chat: "session_chat", tui: "session_tui", cloud: "session_cloud" } as const satisfies Record<string, Surface>;

/**
 * Names which session screen the session route shows, and counts the session as
 * opened once that is known. Set on focus and cleared on unmount only: clearing
 * on blur would show the bare route for a moment and count it as an opening.
 * `form` is null while the route is still loading or showing an error. `key`
 * names the session (host and id) so another session counts as a new opening;
 * it stays on the phone.
 *
 * Reported only while this route is in front: a session left mounted under
 * another one can finish loading out of sight, and is counted when it is
 * shown. Each route instance counts its session once; coming back to it, or a
 * chat/terminal switch, only says it is the session in front again.
 */
export function useSessionSurface(form: keyof typeof VARIANT | null | undefined, key: string): void {
	const variant: Surface | null = form ? VARIANT[form] : null;
	const counted = useRef<string | null>(null);
	useFocusEffect(useCallback(() => {
		if (!variant) return;
		const tracker = uiUsage();
		tracker?.setVariant("session", variant);
		if (counted.current === key) {
			tracker?.sessionReturned(key);
		} else {
			counted.current = key;
			tracker?.sessionOpened(key);
		}
	}, [variant, key]));
	const shown = variant !== null;
	useEffect(() => () => {
		if (shown) uiUsage()?.setVariant("session", null);
	}, [shown]);
}

/**
 * The session screen shows its content: a chat's messages are drawn, or a
 * terminal's first output. Ends the timing of the session's opening. Reported
 * only while the screen is in front, so a session loading out of sight under
 * another cannot end that one's timing.
 */
export function useSessionContentShown(shown: boolean): void {
	useFocusEffect(useCallback(() => {
		if (shown) uiUsage()?.sessionContentShown();
	}, [shown]));
}
