import { useFocusEffect } from "expo-router";
import { useCallback, useEffect } from "react";
import type { Surface } from "./surfaces";
import { uiUsage } from "./uiUsageRegistry";

const VARIANT = { chat: "session_chat", tui: "session_tui", cloud: "session_cloud" } as const satisfies Record<string, Surface>;

/**
 * Names which session screen the session route shows, and counts the session as
 * opened once that is known. Set on focus and cleared on unmount only: clearing
 * on blur would show the bare route for a moment and count it as an opening.
 * `null` while the route is still loading or showing an error.
 */
export function useSessionSurface(form: keyof typeof VARIANT | null | undefined): void {
	const variant: Surface | null = form ? VARIANT[form] : null;
	useFocusEffect(useCallback(() => {
		if (variant) uiUsage()?.setVariant("session", variant);
	}, [variant]));
	const shown = variant !== null;
	useEffect(() => {
		if (shown) uiUsage()?.sessionOpened();
	}, [shown]);
	useEffect(() => () => {
		if (shown) uiUsage()?.setVariant("session", null);
	}, [shown]);
}
