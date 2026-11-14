import { createContext, useContext, useLayoutEffect, useState } from "react";
import { pageHidden } from "../../lib/page-hidden";
import type { ConversationItem } from "../../types/conversation";

/**
 * Entrance motion in a chat plays for content that arrives while the reader is
 * watching, never for content that was already there or that arrived while they
 * were away: not when the chat opens, is switched back to, loads an older page,
 * scrolls a row back into view, catches up after the reader returns (a stale
 * copy refetched, a hibernated agent woken, a remote poll), or while the window
 * is hidden (an animation started then would only play once the reader is back).
 *
 * `floor` is the newest sequence the reader has not watched arrive. It starts at
 * the newest sequence the chat opened with, taken from the first render that has
 * a transcript, and rises past any batch of rows the reader did not watch land
 * (`admitArrivals`). Sequences are conversation-scoped, allocated on insert and
 * never reused, so a row above it arrived while the reader watched, and a row at
 * or below it did not. `settled` turns on after the chat's first commit, for rows
 * with no sequence of their own (the live row). `played` remembers which
 * entrances have run, so a row remounted by windowing or a fold does not play its
 * entrance twice.
 *
 * The gate is plain mutable state read once at mount: nothing re-renders when
 * it changes.
 */
export interface ChatEntranceGate {
	floor: number | undefined;
	settled: boolean;
	readonly played: Set<string>;
	/** The newest sequence `admitArrivals` has accounted for. */
	seen: number | undefined;
	/** When the reader started watching (the chat opened, its panel or the window was seen again), in epoch ms. */
	watchingSince: number;
	/** When rows the reader did not watch last landed (performance.now()); the view snaps for a moment after. */
	catchUpAt: number;
}

/**
 * A row written this long before the reader started watching still counts as
 * arriving while they watch. Covers a remote host whose clock runs a little
 * behind; a local daemon shares this machine's clock.
 */
export const ARRIVAL_CLOCK_SKEW_MS = 5_000;

export function createChatEntranceGate(): ChatEntranceGate {
	return { floor: undefined, settled: false, played: new Set(), seen: undefined, watchingSince: Date.now(), catchUpAt: -Infinity };
}

function writtenBefore(item: ConversationItem, limit: number): boolean {
	const at = Date.parse(item.createdAt);
	return Number.isFinite(at) && at < limit;
}

/**
 * Accounts for the snapshot about to render, before its rows mount. Rows newer
 * than any seen so far are arrivals; when the window is hidden, or any of them
 * was written before the reader started watching, the batch is a catch-up: the
 * floor rises past it so none of it plays an entrance, and `catchUpAt` marks it
 * so the view lands on it without a glide. Returns whether it was a catch-up.
 */
export function admitArrivals(gate: ChatEntranceGate, snapshot: { items: readonly ConversationItem[]; latestSequence: number }): boolean {
	if (snapshot.items.length === 0) return false;
	// Runs on every render of the transcript (each streamed chunk): nothing new is the
	// common case, and it is decided without walking the rows.
	const seenSoFar = gate.seen ?? gate.floor;
	if (seenSoFar !== undefined && Math.max(snapshot.latestSequence, snapshot.items[snapshot.items.length - 1]!.sequence) <= seenSoFar) return false;
	const newest = snapshot.items.reduce((max, item) => Math.max(max, item.sequence), snapshot.latestSequence);
	if (gate.floor === undefined) {
		gate.floor = newest;
		gate.seen = newest;
		return false;
	}
	const seen = gate.seen ?? gate.floor;
	if (newest <= seen) return false;
	gate.seen = newest;
	const watchedFrom = gate.watchingSince - ARRIVAL_CLOCK_SKEW_MS;
	const catchUp = pageHidden() || snapshot.items.some((item) => item.sequence > seen && writtenBefore(item, watchedFrom));
	if (!catchUp) return false;
	gate.floor = Math.max(gate.floor, newest);
	gate.catchUpAt = performance.now();
	return true;
}

/** Without a gate (a row rendered outside a chat timeline), entrances play as they always did. */
export const ChatEntrance = createContext<ChatEntranceGate | null>(null);

/**
 * Whether this row plays its entrance. Decided once, when the row mounts. Give
 * the newest sequence the row shows; omit it for a row with none, which then
 * enters only if it mounts after the chat opened. A row that mounts while the
 * window is hidden never enters.
 */
export function useChatEntrance(key: string, sequence?: number): boolean {
	const gate = useContext(ChatEntrance);
	const [entering] = useState(() => {
		if (!gate) return true;
		if (gate.played.has(key) || pageHidden()) return false;
		if (sequence === undefined) return gate.settled;
		return gate.floor !== undefined && sequence > gate.floor;
	});
	useLayoutEffect(() => {
		gate?.played.add(key);
	}, [gate, key]);
	return entering;
}
