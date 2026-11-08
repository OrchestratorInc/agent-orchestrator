import { useEffect, useState } from "react";
import type { SessionInterfaceTransition } from "../chat/api";
import { interfaceHandoffPending, mobileInterfaceTransitionIsActive } from "./interfaceTransition";

/**
 * How long a finished switch waits for the route to swap the screen out before
 * the card offers a way on. The board polls every few seconds, so a healthy
 * handoff lands well inside this; past it the route is not going to catch up on
 * its own (a session resolved by a one-off lookup, or a board refresh that
 * keeps failing).
 */
export const INTERFACE_HANDOFF_WAIT_MS = 10_000;

export type InterfaceHandoff = {
	/** A switch away from this screen that it watched run has completed. */
	pending: boolean;
	/** `pending` has outlasted `INTERFACE_HANDOFF_WAIT_MS`. */
	stalled: boolean;
};

/**
 * True once a switch away from `screenMode` that this screen watched run has
 * completed, until the route swaps the screen out. See `interfaceHandoffPending`.
 * `stalled` bounds the wait so the card never spins forever with no action.
 */
export function useInterfaceHandoff(transition: SessionInterfaceTransition | undefined, screenMode: "chat" | "tui"): InterfaceHandoff {
	const [watchedId, setWatchedId] = useState<string>();
	const [stalledId, setStalledId] = useState<string>();
	const leavingId = transition && transition.targetMode !== screenMode && mobileInterfaceTransitionIsActive(transition) ? transition.id : undefined;
	useEffect(() => {
		if (leavingId) setWatchedId(leavingId);
	}, [leavingId]);
	const pending = interfaceHandoffPending(transition, screenMode, watchedId);
	const pendingId = pending ? transition?.id : undefined;
	useEffect(() => {
		if (!pendingId) return;
		const timer = setTimeout(() => setStalledId(pendingId), INTERFACE_HANDOFF_WAIT_MS);
		return () => clearTimeout(timer);
	}, [pendingId]);
	return { pending, stalled: pendingId !== undefined && stalledId === pendingId };
}
