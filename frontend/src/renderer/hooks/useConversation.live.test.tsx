import { renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, type ReactNode } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));

vi.mock("../lib/host-clients", async (importOriginal) => ({
	...await importOriginal<typeof import("../lib/host-clients")>(),
	clientForSessionHost: () => ({ GET: getMock }),
}));

import { useConversation, useConversationConfigOptions } from "./useConversation";
import { setEventsConnectionState, setRemoteEventsHealthy } from "../lib/events-connection";

const snapshot = { conversationId: "c1", sessionId: "s1", mode: "chat", controller: "ready", latestSequence: 1, messages: [], activities: [] };

function wrapper({ children }: { children: ReactNode }) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

const advance = (ms: number) => act(async () => { await vi.advanceTimersByTimeAsync(ms); });
const reads = (path: string) => getMock.mock.calls.filter(([called]) => called === path).length;

beforeEach(() => {
	vi.useFakeTimers();
	getMock.mockReset().mockImplementation(async (path: string) => ({ data: path.endsWith("/config-options") ? { options: [] } : snapshot }));
});

afterEach(() => {
	setRemoteEventsHealthy("box", false);
	setEventsConnectionState("idle");
	vi.useRealTimers();
});

it("polls an open remote chat only while the host's event stream is not delivering", async () => {
	const path = "/api/v1/sessions/{sessionId}/conversation";
	renderHook(() => useConversation("s1", "box"), { wrapper });
	await advance(0);
	expect(reads(path)).toBe(1);
	await advance(2_000);
	expect(reads(path)).toBe(2);

	act(() => setRemoteEventsHealthy("box", true));
	await advance(10_000);
	expect(reads(path)).toBe(2);

	act(() => setRemoteEventsHealthy("box", false));
	await advance(2_000);
	expect(reads(path)).toBe(3);
});

it("slows the config-option poll while the local event stream is connected", async () => {
	const path = "/api/v1/sessions/{sessionId}/conversation/config-options";
	renderHook(() => useConversationConfigOptions("s1", true), { wrapper });
	await advance(0);
	expect(reads(path)).toBe(1);
	await advance(5_000);
	expect(reads(path)).toBe(2);

	act(() => setEventsConnectionState("connected"));
	await advance(25_000);
	expect(reads(path)).toBe(2);
	await advance(5_000);
	expect(reads(path)).toBe(3);
});
