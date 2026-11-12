import { createContext, useContext, useLayoutEffect, useState } from "react";

/**
 * Entrance motion in a chat plays for content that arrives while the chat is on
 * screen, never for content that was already there: not when the chat opens,
 * is switched back to, loads an older page, or scrolls a row back into view.
 *
 * `floor` is the newest sequence the chat opened with, taken from the first
 * render that has a transcript. Sequences are conversation-scoped and never
 * reused, so a row above it arrived after the chat opened, and a row at or
 * below it (history, an older page) did not. `settled` turns on after the
 * chat's first commit, for rows with no sequence of their own (the live row).
 * `played` remembers which entrances have run, so a row remounted by windowing
 * or a fold does not play its entrance twice.
 *
 * The gate is plain mutable state read once at mount: nothing re-renders when
 * it changes.
 */
export interface ChatEntranceGate {
	floor: number | undefined;
	settled: boolean;
	readonly played: Set<string>;
}

export function createChatEntranceGate(): ChatEntranceGate {
	return { floor: undefined, settled: false, played: new Set() };
}

/** Without a gate (a row rendered outside a chat timeline), entrances play as they always did. */
export const ChatEntrance = createContext<ChatEntranceGate | null>(null);

/**
 * Whether this row plays its entrance. Decided once, when the row mounts. Give
 * the newest sequence the row shows; omit it for a row with none, which then
 * enters only if it mounts after the chat opened.
 */
export function useChatEntrance(key: string, sequence?: number): boolean {
	const gate = useContext(ChatEntrance);
	const [entering] = useState(() => {
		if (!gate) return true;
		if (gate.played.has(key)) return false;
		if (sequence === undefined) return gate.settled;
		return gate.floor !== undefined && sequence > gate.floor;
	});
	useLayoutEffect(() => {
		gate?.played.add(key);
	}, [gate, key]);
	return entering;
}
