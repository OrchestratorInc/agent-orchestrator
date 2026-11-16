import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";

const { getMocks, connected } = vi.hoisted(() => ({
	getMocks: new Map<string, ReturnType<typeof vi.fn>>(),
	connected: { hosts: [] as string[] },
}));

// Each remote machine has its own client, reached through the desktop's
// connection to it; the local daemon is never asked by this hook.
vi.mock("../lib/host-clients", async (importOriginal) => ({
	...await importOriginal<typeof import("../lib/host-clients")>(),
	clientForHost: (hostId: string) => ({ GET: getMocks.get(hostId) }),
	labelForHost: (hostId: string) => ({ h_air: "MacBook Air", h_box: "Build box" } as Record<string, string>)[hostId],
}));
vi.mock("./useHostConnection", () => ({ useConnectedHosts: () => connected.hosts }));

import { publishSavedRemoteHosts } from "../lib/saved-remote-hosts";
import { useRemoteMachineSearch } from "./useRemoteMachineSearch";

function wrapper({ children }: { children: ReactNode }) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

const ok = (sessionId: string) => ({
	data: { query: "flaky", results: [{ sessionId, projectId: "p", harness: "codex", kind: "user", seq: 1, snippet: "flaky", rank: 0, createdAt: "2026-10-01T00:00:00Z" }], total: 3, limit: 8, offset: 0, sort: "relevance", coverage: { state: "ready" } },
	response: { ok: true, status: 200 },
});

describe("useRemoteMachineSearch", () => {
	beforeEach(() => {
		getMocks.clear();
		connected.hosts = [];
		publishSavedRemoteHosts([]);
	});
	afterEach(() => {
		vi.useRealTimers();
	});

	it("asks no one without words, or while disabled", () => {
		connected.hosts = ["h_air"];
		getMocks.set("h_air", vi.fn());
		const idle = renderHook(() => useRemoteMachineSearch({ query: "  ", limit: 8 }, true), { wrapper });
		expect(idle.result.current).toEqual({ query: "", answers: [], settled: true });
		renderHook(() => useRemoteMachineSearch({ query: "flaky", limit: 8 }, false), { wrapper });
		expect(getMocks.get("h_air")).not.toHaveBeenCalled();
	});

	it("asks every connected machine the same words, and labels each answer", async () => {
		connected.hosts = ["h_air", "h_box"];
		getMocks.set("h_air", vi.fn().mockResolvedValue(ok("air-1")));
		getMocks.set("h_box", vi.fn().mockResolvedValue(ok("box-1")));
		const { result } = renderHook(
			() => useRemoteMachineSearch({ query: " flaky harness:codex ", limit: 25 }, true),
			{ wrapper },
		);
		await waitFor(() => expect(result.current.settled).toBe(true));
		expect(getMocks.get("h_air")).toHaveBeenCalledWith("/api/v1/search/sessions", expect.objectContaining({
			params: { query: { q: "flaky harness:codex", limit: 25 } },
		}));
		expect(result.current.answers).toMatchObject([
			{ hostId: "h_air", label: "MacBook Air", outcome: "searched", total: 3, results: [{ sessionId: "air-1" }] },
			{ hostId: "h_box", label: "Build box", outcome: "searched", results: [{ sessionId: "box-1" }] },
		]);
		// The answers say which words they are for, so a caller never mixes them
		// with another query's.
		expect(result.current.query).toBe("flaky harness:codex");
	});

	// Another machine's AO is the person's own, but may be older, newer or broken.
	it("bounds what a machine sends: no more rows than asked, no oversized snippet", async () => {
		connected.hosts = ["h_air"];
		const big = { sessionId: "big", projectId: "p", harness: "codex", kind: "user", seq: 1, snippet: `flaky ${"x".repeat(50_000)}`, highlights: [{ start: 0, end: 5 }], rank: 0, createdAt: "2026-10-01T00:00:00Z" };
		getMocks.set("h_air", vi.fn().mockResolvedValue({
			data: { query: "flaky", results: Array.from({ length: 30 }, (_, index) => ({ ...big, sessionId: `big-${index}` })), total: 30, limit: 8, offset: 0, sort: "relevance", coverage: { state: "ready" } },
			response: { ok: true, status: 200 },
		}));
		const { result } = renderHook(() => useRemoteMachineSearch({ query: "flaky", limit: 8 }, true), { wrapper });
		await waitFor(() => expect(result.current.settled).toBe(true));
		const [air] = result.current.answers;
		expect(air?.results).toHaveLength(8);
		expect(air?.results[0]?.snippet.length).toBeLessThanOrEqual(302);
		expect(air?.results[0]?.highlights).toEqual([{ start: 0, end: 5 }]);
	});

	it("reports a machine that cannot answer, and why, without failing the others", async () => {
		connected.hosts = ["h_air", "h_box"];
		// An AO without session search, and a machine whose tunnel is gone.
		getMocks.set("h_air", vi.fn().mockResolvedValue({ error: { message: "not found" }, response: { ok: false, status: 404 } }));
		getMocks.set("h_box", vi.fn().mockRejectedValue(new TypeError("Failed to fetch")));
		const { result } = renderHook(() => useRemoteMachineSearch({ query: "flaky", limit: 8 }, true), { wrapper });
		await waitFor(() => expect(result.current.settled).toBe(true));
		expect(result.current.answers.map((answer) => [answer.label, answer.outcome])).toEqual([
			["MacBook Air", "needs_update"],
			["Build box", "offline"],
		]);
	});

	it("says a machine's search is turned off rather than that it failed", async () => {
		connected.hosts = ["h_air"];
		getMocks.set("h_air", vi.fn().mockResolvedValue({
			error: { code: "SESSION_SEARCH_UNAVAILABLE", message: "Session search is not available on this daemon" },
			response: { ok: false, status: 503 },
		}));
		const { result } = renderHook(() => useRemoteMachineSearch({ query: "flaky", limit: 8 }, true), { wrapper });
		await waitFor(() => expect(result.current.settled).toBe(true));
		expect(result.current.answers).toMatchObject([{ label: "MacBook Air", outcome: "search_off" }]);
	});

	it("gives a slow machine six seconds, then reports it", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		connected.hosts = ["h_air"];
		getMocks.set("h_air", vi.fn((_path: string, init: { signal: AbortSignal }) => new Promise((_, reject) => {
			init.signal.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
		})));
		const { result } = renderHook(() => useRemoteMachineSearch({ query: "flaky", limit: 8 }, true), { wrapper });
		await waitFor(() => expect(getMocks.get("h_air")).toHaveBeenCalled());
		expect(result.current.settled).toBe(false);
		await act(async () => {
			await vi.advanceTimersByTimeAsync(6_000);
		});
		await waitFor(() => expect(result.current.settled).toBe(true));
		expect(result.current.answers).toMatchObject([{ label: "MacBook Air", outcome: "timed_out" }]);
	});

	it("reports saved machines that are not connected without asking them", async () => {
		connected.hosts = ["h_air"];
		getMocks.set("h_air", vi.fn().mockResolvedValue(ok("air-1")));
		publishSavedRemoteHosts([
			{ hostId: "h_air", label: "MacBook Air", status: "connected" },
			{ hostId: "h_home", label: "Laptop at home", status: "offline" },
			{ hostId: "h_new", label: "New box", status: "connecting" },
			// The connection ended because the machine refused the saved password,
			// or runs an AO this desktop cannot talk to.
			{ hostId: "h_locked", label: "Locked box", status: "offline", failureReason: "unauthorized" },
			{ hostId: "h_old", label: "Old box", status: "offline", failureReason: "incompatible" },
		]);
		const { result } = renderHook(() => useRemoteMachineSearch({ query: "flaky", limit: 8 }, true), { wrapper });
		await waitFor(() => expect(result.current.settled).toBe(true));
		expect(result.current.answers.map((answer) => [answer.label, answer.outcome])).toEqual([
			["MacBook Air", "searched"],
			["Laptop at home", "offline"],
			["New box", "connecting"],
			["Locked box", "unauthorized"],
			["Old box", "needs_update"],
		]);
	});
});
