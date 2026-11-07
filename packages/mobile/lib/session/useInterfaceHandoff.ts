import { useEffect, useState } from "react";
import type { SessionInterfaceTransition } from "../chat/api";
import { interfaceHandoffPending, mobileInterfaceTransitionIsActive } from "./interfaceTransition";

/**
 * True once a switch away from `screenMode` that this screen watched run has
 * completed, until the route swaps the screen out. See `interfaceHandoffPending`.
 */
export function useInterfaceHandoff(transition: SessionInterfaceTransition | undefined, screenMode: "chat" | "tui"): boolean {
	const [watchedId, setWatchedId] = useState<string>();
	const leavingId = transition && transition.targetMode !== screenMode && mobileInterfaceTransitionIsActive(transition) ? transition.id : undefined;
	useEffect(() => {
		if (leavingId) setWatchedId(leavingId);
	}, [leavingId]);
	return interfaceHandoffPending(transition, screenMode, watchedId);
}
