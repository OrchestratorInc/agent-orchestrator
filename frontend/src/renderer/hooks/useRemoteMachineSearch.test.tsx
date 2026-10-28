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
		const idle = renderHook(() => useRemoteMachineSearch({ query: "  ", harness: "", from: "", to: "", limit: 8 }, true), { wrapper });
		expect(idle.result.current).toEqual({ answers: [], settled: true });
		renderHook(() => useRemoteMachineSearch({ query: "flaky", harness: "", from: "", to: "", limit: 8 }, false), { wrapper });
		expect(getMocks.get("h_air")).not.toHaveBeenCalled();
	});

	it("asks every connected machine the same words and filters, and labels each answer", async () => {
		connected.hosts = ["h_air", "h_box"];
		getMocks.set("h_air", vi.fn().mockResolvedValue(ok("air-1")));
		getMocks.set("h_box", vi.fn().mockResolvedValue(ok("box-1")));
		const { result } = renderHook(
			() => useRemoteMachineSearch({ query: " flaky ", harness: "codex", from: "2026-01-01", to: "", sort: "recent", limit: 25 }, true),
			{ wrapper },
		);
		await waitFor(() => expect(result.current.settled).toBe(true));
		expect(getMocks.get("h_air")).toHaveBeenCalledWith("/api/v1/search/sessions", expect.objectContaining({
			params: { query: { q: "flaky", harness: "codex", from: "2026-01-01", sort: "recent", limit: 25 } },
		}));
		expect(result.current.answers).toMatchObject([
			{ hostId: "h_air", label: "MacBook Air", outcome: "searched", total: 3, results: [{ sessionId: "air-1" }] },
			{ hostId: "h_box", label: "Build box", outcome: "searched", results: [{ sessionId: "box-1" }] },
		]);
	});

	it("reports a machine that cannot answer, and why, without failing the others", async () => {
		connected.hosts = ["h_air", "h_box"];
		// An AO without session search, and a machine whose tunnel is gone.
		getMocks.set("h_air", vi.fn().mockResolvedValue({ error: { message: "not found" }, response: { ok: false, status: 404 } }));
		getMocks.set("h_box", vi.fn().mockRejectedValue(new TypeError("Failed to fetch")));
		const { result } = renderHook(() => useRemoteMachineSearch({ query: "flaky", harness: "", from: "", to: "", limit: 8 }, true), { wrapper });
		await waitFor(() => expect(result.current.settled).toBe(true));
		expect(result.current.answers.map((answer) => [answer.label, answer.outcome])).toEqual([
			["MacBook Air", "needs_update"],
			["Build box", "offline"],
		]);
	});

	it("gives a slow machine six seconds, then reports it", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		connected.hosts = ["h_air"];
		getMocks.set("h_air", vi.fn((_path: string, init: { signal: AbortSignal }) => new Promise((_, reject) => {
			init.signal.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
		})));
		const { result } = renderHook(() => useRemoteMachineSearch({ query: "flaky", harness: "", from: "", to: "", limit: 8 }, true), { wrapper });
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
		]);
		const { result } = renderHook(() => useRemoteMachineSearch({ query: "flaky", harness: "", from: "", to: "", limit: 8 }, true), { wrapper });
		await waitFor(() => expect(result.current.settled).toBe(true));
		expect(result.current.answers.map((answer) => [answer.label, answer.outcome])).toEqual([
			["MacBook Air", "searched"],
			["Laptop at home", "offline"],
			["New box", "connecting"],
		]);
	});
});
