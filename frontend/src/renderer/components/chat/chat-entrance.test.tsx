import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it } from "vitest";
import type { ConversationItem } from "../../types/conversation";
import { admitArrivals, ARRIVAL_CLOCK_SKEW_MS, ChatEntrance, createChatEntranceGate, useChatEntrance, useRequestedEntrance, type ChatEntranceGate } from "./chat-entrance";

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

	it("treats rows a kept chat holds when it is shown again as a catch-up", () => {
		// KeepRecentAlive: written while another session was on screen, after the chat first opened.
		const gate = gateAt(NOW);
		admitArrivals(gate, snapshot(row(1, NOW)));
		expect(admitArrivals(gate, snapshot(row(1, NOW), row(2, NOW + 60_000)), true)).toBe(true);
		expect(gate.floor).toBe(2);
		// Shown again, the reader watches: a row written then still enters.
		expect(admitArrivals(gate, snapshot(row(1, NOW), row(2, NOW + 60_000), row(3, NOW + 61_000)))).toBe(false);
		expect(gate.floor).toBe(2);
	});

	it("treats rows written just before the reader came back as a catch-up, with no skew allowance", () => {
		// A to B to A in two seconds: the open read of what A wrote meanwhile lands after A is back.
		const gate = gateAt(NOW - 60_000);
		admitArrivals(gate, snapshot(row(1, NOW - 60_000)));
		gate.watchingSince = gate.returnedAt = NOW;
		expect(admitArrivals(gate, snapshot(row(1, NOW - 60_000), row(2, NOW - 2_000)))).toBe(true);
		expect(gate.floor).toBe(2);
		expect(admitArrivals(gate, snapshot(row(1, NOW - 60_000), row(2, NOW - 2_000), row(3, NOW + 500)))).toBe(false);
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

describe("useRequestedEntrance", () => {
	const withGate = (gate: ChatEntranceGate) => ({ children }: { children: ReactNode }) => (
		<ChatEntrance.Provider value={gate}>{children}</ChatEntrance.Provider>
	);

	it("plays when asked after the row mounted, and keeps playing through re-renders", () => {
		const gate = createChatEntranceGate();
		const hook = renderHook(({ requested }) => useRequestedEntrance("prompt", requested), {
			initialProps: { requested: false },
			wrapper: withGate(gate),
		});
		expect(hook.result.current).toBe(false);
		hook.rerender({ requested: true });
		expect(hook.result.current).toBe(true);
		hook.rerender({ requested: true });
		expect(hook.result.current).toBe(true);
	});

	it("does not play again for a row remounted with the flag still set", () => {
		// The timeline windows rows out and back in; the sent prompt keeps its flag until the next send.
		const gate = createChatEntranceGate();
		renderHook(() => useRequestedEntrance("prompt", true), { wrapper: withGate(gate) }).unmount();
		expect(renderHook(() => useRequestedEntrance("prompt", true), { wrapper: withGate(gate) }).result.current).toBe(false);
		expect(renderHook(() => useRequestedEntrance("other", true), { wrapper: withGate(gate) }).result.current).toBe(true);
	});

	it("plays whenever asked outside a chat timeline", () => {
		renderHook(() => useRequestedEntrance("prompt", true)).unmount();
		expect(renderHook(() => useRequestedEntrance("prompt", true)).result.current).toBe(true);
	});
});
