import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it } from "vitest";
import type { ConversationItem } from "../../types/conversation";
import { admitArrivals, ARRIVAL_CLOCK_SKEW_MS, ChatEntrance, createChatEntranceGate, useChatEntrance, type ChatEntranceGate } from "./chat-entrance";

const NOW = Date.parse("2026-11-12T10:00:00Z");

function row(sequence: number, createdAt: number): ConversationItem {
	return {
		kind: "message", id: `m${sequence}`, turnId: "t1", sequence, revision: 0,
		role: "assistant", origin: "provider", text: "", streaming: false, createdAt: new Date(createdAt).toISOString(),
	};
}

function snapshot(...items: ConversationItem[]) {
	return { items, latestSequence: items.reduce((max, item) => Math.max(max, item.sequence), 0) };
}

function gateAt(watchingSince: number): ChatEntranceGate {
	return { ...createChatEntranceGate(), watchingSince };
}

function setVisibility(state: DocumentVisibilityState) {
	Object.defineProperty(document, "visibilityState", { configurable: true, get: () => state });
}

afterEach(() => {
	Reflect.deleteProperty(document, "visibilityState");
});

describe("admitArrivals", () => {
	it("takes the first transcript as history", () => {
		const gate = gateAt(NOW);
		expect(admitArrivals(gate, snapshot())).toBe(false);
		expect(gate.floor).toBeUndefined();
		expect(admitArrivals(gate, snapshot(row(1, NOW - 60_000), row(2, NOW - 30_000)))).toBe(false);
		expect(gate.floor).toBe(2);
	});

	it("leaves the floor for rows written while the reader watches", () => {
		const gate = gateAt(NOW);
		admitArrivals(gate, snapshot(row(1, NOW - 60_000)));
		expect(admitArrivals(gate, snapshot(row(1, NOW - 60_000), row(2, NOW + 1_000)))).toBe(false);
		expect(gate.floor).toBe(1);
	});

	it("raises the floor past rows written before the reader started watching", () => {
		// A stale copy refetched on return, or a hibernated chat woken: rows from while they were away.
		const gate = gateAt(NOW);
		admitArrivals(gate, snapshot(row(1, NOW - 20 * 60_000)));
		const caughtUp = snapshot(row(1, NOW - 20 * 60_000), row(2, NOW - 10 * 60_000), row(3, NOW + 500));
		expect(admitArrivals(gate, caughtUp)).toBe(true);
		expect(gate.floor).toBe(3);
		// The same snapshot again changes nothing.
		expect(admitArrivals(gate, caughtUp)).toBe(false);
	});

	it("allows a remote clock a little behind", () => {
		const gate = gateAt(NOW);
		admitArrivals(gate, snapshot(row(1, NOW - 60_000)));
		expect(admitArrivals(gate, snapshot(row(1, NOW - 60_000), row(2, NOW - ARRIVAL_CLOCK_SKEW_MS + 100)))).toBe(false);
		expect(gate.floor).toBe(1);
	});

	it("treats anything landing while the window is hidden as a catch-up", () => {
		const gate = gateAt(NOW);
		admitArrivals(gate, snapshot(row(1, NOW)));
		setVisibility("hidden");
		expect(admitArrivals(gate, snapshot(row(1, NOW), row(2, NOW + 5_000)))).toBe(true);
		expect(gate.floor).toBe(2);
	});

	it("ignores an older page loading above the transcript", () => {
		const gate = gateAt(NOW);
		admitArrivals(gate, snapshot(row(10, NOW - 60_000)));
		expect(admitArrivals(gate, snapshot(row(4, NOW - 99 * 60_000), row(10, NOW - 60_000)))).toBe(false);
		expect(gate.floor).toBe(10);
	});
});

describe("useChatEntrance", () => {
	const withGate = (gate: ChatEntranceGate) => ({ children }: { children: ReactNode }) => (
		<ChatEntrance.Provider value={gate}>{children}</ChatEntrance.Provider>
	);

	it("enters a row above the floor", () => {
		const gate = { ...createChatEntranceGate(), floor: 1, settled: true };
		expect(renderHook(() => useChatEntrance("row:a", 2), { wrapper: withGate(gate) }).result.current).toBe(true);
	});

	it("never enters a row that mounts while the window is hidden", () => {
		const gate = { ...createChatEntranceGate(), floor: 1, settled: true };
		setVisibility("hidden");
		expect(renderHook(() => useChatEntrance("row:a", 2), { wrapper: withGate(gate) }).result.current).toBe(false);
		expect(renderHook(() => useChatEntrance("live"), { wrapper: withGate(gate) }).result.current).toBe(false);
	});
});
