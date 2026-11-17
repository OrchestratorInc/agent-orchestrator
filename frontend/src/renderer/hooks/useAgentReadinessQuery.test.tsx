import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { agentReadiness } from "../test/agent-readiness-fixtures";

const { getMock, postMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn(),
}));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, POST: postMock },
	apiErrorMessage: () => "request failed",
}));

import {
	agentReadinessQueryOptions,
	agentReadinessQueryKey,
	mergeAgentReadiness,
	useAgentReadinessQuery,
	useEnsureAgentReadiness,
	useStandaloneAgentReadiness,
} from "./useAgentReadinessQuery";

function wrapper(queryClient: QueryClient) {
	return ({ children }: { children: ReactNode }) => (
		<QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
	);
}

beforeEach(() => {
	getMock.mockReset().mockResolvedValue({
		data: { agents: [agentReadiness("codex", "Codex")] },
		error: undefined,
	});
	postMock.mockReset().mockResolvedValue({
		data: { agents: [agentReadiness("codex", "Codex")] },
		error: undefined,
	});
});

describe("agent readiness query", () => {
	it("follows pending daemon recovery using cached reads, then stops when ready", async () => {
		vi.useFakeTimers();
		try {
			const pending = { agents: [agentReadiness("codex", "Codex", { authentication: "unknown" })] };
			getMock.mockResolvedValue({ data: pending });
			postMock.mockResolvedValue({ data: pending });
			const client = new QueryClient();
			const { result, rerender, unmount } = renderHook(({ pending }) => useStandaloneAgentReadiness("codex", pending, true), {
				initialProps: { pending: true }, wrapper: wrapper(client),
			});
			await act(async () => { await vi.advanceTimersByTimeAsync(0); });
			expect(postMock).toHaveBeenCalledTimes(1);
			expect(result.current.timedOut).toBe(false);
			getMock.mockResolvedValue({ data: { agents: [agentReadiness("codex", "Codex")] } });
			await act(async () => { await vi.advanceTimersByTimeAsync(2_000); });
			expect(client.getQueryData(agentReadinessQueryKey)).toMatchObject({ agents: [{ authentication: { state: "authorized" } }] });
			rerender({ pending: false });
			const reads = getMock.mock.calls.length;
			await act(async () => { await vi.advanceTimersByTimeAsync(65_000); });
			expect(getMock).toHaveBeenCalledTimes(reads);
			expect(postMock).toHaveBeenCalledTimes(1);
			unmount();
		} finally { vi.useRealTimers(); }
	});

	it("bounds pending polling to 60 seconds and coalesces explicit Retry probes", async () => {
		vi.useFakeTimers();
		try {
			const pending = { agents: [agentReadiness("codex", "Codex", { authentication: "unknown" })] };
			getMock.mockResolvedValue({ data: pending });
			postMock.mockResolvedValue({ data: pending });
			const { result, unmount } = renderHook(() => useStandaloneAgentReadiness("codex", true, true), { wrapper: wrapper(new QueryClient()) });
			await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
			expect(result.current.timedOut).toBe(true);
			const reads = getMock.mock.calls.length;
			await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
			expect(getMock).toHaveBeenCalledTimes(reads);
			let finish!: (response: unknown) => void;
			postMock.mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
			act(() => { result.current.retry(); result.current.retry(); });
			expect(postMock).toHaveBeenCalledTimes(2);
			expect(postMock).toHaveBeenLastCalledWith("/api/v1/agents/{agent}/probe", { params: { path: { agent: "codex" } } });
			expect(result.current.timedOut).toBe(false);
			await act(async () => { finish({ data: {} }); });
			unmount();
		} finally { vi.useRealTimers(); }
	});

	it("ignores an ensure response after leaving the standalone view", async () => {
		let finish!: (response: unknown) => void;
		postMock.mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
		const client = new QueryClient();
		const { unmount } = renderHook(() => useStandaloneAgentReadiness("codex", true, true), { wrapper: wrapper(client) });
		await act(async () => { await Promise.resolve(); });
		unmount();
		client.setQueryData(agentReadinessQueryKey, { agents: [agentReadiness("claude-code")] });
		await act(async () => { finish({ data: { agents: [agentReadiness("codex")] } }); });
		expect(client.getQueryData(agentReadinessQueryKey)).toEqual({ agents: [agentReadiness("claude-code")] });
	});

	it("discards an old daemon's completion and restarts recovery on identity replacement", async () => {
		getMock.mockResolvedValue({ data: { agents: [agentReadiness("codex", "Codex", { authentication: "unknown" })] } });
		const finishes: Array<(response: unknown) => void> = [];
		postMock.mockImplementation(() => new Promise((resolve) => { finishes.push(resolve); }));
		const client = new QueryClient();
		const { unmount } = renderHook(() => useStandaloneAgentReadiness("codex", true, true), { wrapper: wrapper(client) });
		await act(async () => { await Promise.resolve(); });
		act(() => { client.removeQueries({ queryKey: agentReadinessQueryKey, exact: true }); });
		expect(finishes).toHaveLength(2);
		client.setQueryData(agentReadinessQueryKey, { agents: [agentReadiness("codex", "Codex", { authentication: "unknown" })] });
		await act(async () => { finishes[0]({ data: { agents: [agentReadiness("codex", "Codex")] } }); });
		expect(client.getQueryData(agentReadinessQueryKey)).toMatchObject({ agents: [{ authentication: { state: "unknown" } }] });
		await act(async () => { finishes[1]({ data: { agents: [agentReadiness("codex", "Codex")] } }); });
		expect(client.getQueryData(agentReadinessQueryKey)).toMatchObject({ agents: [{ authentication: { state: "authorized" } }] });
		unmount();
	});

	it("does no recovery work for ready agents or other creation paths", () => {
		const first = renderHook(() => useStandaloneAgentReadiness("codex", false, true), { wrapper: wrapper(new QueryClient()) });
		const second = renderHook(() => useStandaloneAgentReadiness("codex", true, false), { wrapper: wrapper(new QueryClient()) });
		expect(getMock).not.toHaveBeenCalled();
		expect(postMock).not.toHaveBeenCalled();
		first.unmount(); second.unmount();
	});

	it("leaves freshness policy to the daemon", () => {
		expect(agentReadinessQueryOptions.staleTime).toBe(Number.POSITIVE_INFINITY);
		expect(agentReadinessQueryOptions.gcTime).toBe(Number.POSITIVE_INFINITY);
	});

	it("reads the cached daemon snapshot without invoking ensure", async () => {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const { result } = renderHook(() => useAgentReadinessQuery(), { wrapper: wrapper(queryClient) });

		await waitFor(() => expect(result.current.data?.agents[0]?.id).toBe("codex"));
		expect(getMock).toHaveBeenCalledWith("/api/v1/agents/readiness");
		expect(postMock).not.toHaveBeenCalled();
	});

	it("ensures normalized relevant harness ids and updates the display copy", async () => {
		const queryClient = new QueryClient();
		renderHook(
			() => useEnsureAgentReadiness({ agentIds: ["codex", "claude-code", "codex"] }),
			{ wrapper: wrapper(queryClient) },
		);

		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/agents/readiness/ensure", {
				body: { agentIds: ["claude-code", "codex"], purpose: "display" },
			}),
		);
		expect(queryClient.getQueryData(agentReadinessQueryKey)).toEqual({
			agents: [agentReadiness("codex", "Codex")],
		});
	});

	it("merges targeted ensures without discarding other harness snapshots", () => {
		const claude = agentReadiness("claude-code", "Claude Code");
		const staleCodex = agentReadiness("codex", "Codex", { freshness: "stale" });
		const freshCodex = agentReadiness("codex", "Codex");

		expect(mergeAgentReadiness({ agents: [claude, staleCodex] }, { agents: [freshCodex] })).toEqual({
			agents: [claude, freshCodex],
		});
	});

	it("does not let a previous enabled period complete a newer ensure", async () => {
		const resolvers: Array<(value: unknown) => void> = [];
		postMock.mockImplementation(() => new Promise((resolve) => resolvers.push(resolve)));
		const queryClient = new QueryClient();
		const { result, rerender, unmount } = renderHook(
			({ enabled }) => useEnsureAgentReadiness({ enabled }),
			{ initialProps: { enabled: true }, wrapper: wrapper(queryClient) },
		);
		expect(result.current).toBe(false);
		rerender({ enabled: false });
		rerender({ enabled: true });
		const response = { data: { agents: [agentReadiness("codex")] } };
		await act(async () => resolvers[0](response));
		expect(result.current).toBe(false);
		expect(queryClient.getQueryData(agentReadinessQueryKey)).toBeUndefined();
		await act(async () => resolvers[1](response));
		expect(result.current).toBe(true);
		rerender({ enabled: false });
		expect(result.current).toBe(false);
		unmount();
	});

	it("retries failed startup readiness without reporting completion early", async () => {
		vi.useFakeTimers();
		try {
			postMock.mockRejectedValueOnce(new Error("offline"));
			const { result, unmount } = renderHook(() => useEnsureAgentReadiness({ retryOnError: true }), { wrapper: wrapper(new QueryClient()) });
			await act(async () => { await Promise.resolve(); });
			expect(result.current).toBe(false);
			await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
			expect(postMock).toHaveBeenCalledTimes(2);
			expect(result.current).toBe(true);
			unmount();
		} finally { vi.useRealTimers(); }
	});
});
