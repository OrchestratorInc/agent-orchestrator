import { createContext, useContext, useLayoutEffect, useState } from "react";
import { pageHidden } from "../../lib/page-hidden";
import type { ConversationItem } from "../../types/conversation";

export interface ChatEntranceGate {
	floor: number | undefined;
	settled: boolean;
	readonly played: Set<string>;
	seen: number | undefined;
	watchingSince: number;
	catchUpAt: number;
}

export const ARRIVAL_CLOCK_SKEW_MS = 5_000;

export function createChatEntranceGate(): ChatEntranceGate {
	return { floor: undefined, settled: false, played: new Set(), seen: undefined, watchingSince: Date.now(), catchUpAt: -Infinity };
}

function writtenBefore(item: ConversationItem, limit: number): boolean {
	const at = Date.parse(item.createdAt);
	return Number.isFinite(at) && at < limit;
}

export function admitArrivals(gate: ChatEntranceGate, snapshot: { items: readonly ConversationItem[]; latestSequence: number }): boolean {
	if (snapshot.items.length === 0) return false;
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

export const ChatEntrance = createContext<ChatEntranceGate | null>(null);

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
