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
import { isPendingAgent } from "../lib/agent-select-options";

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
	it.each(["unknown", "unauthorized"] as const)("rechecks unknown installation with %s authentication without a remembered agent", async (authentication) => {
		vi.useFakeTimers();
		try {
			const pending = { agents: [agentReadiness("codex", "Codex", { installation: "unknown", authentication }), agentReadiness("aider", "Aider", { installation: "not_installed" }), agentReadiness("claude-code", "Claude Code")] };
			getMock.mockResolvedValue({ data: pending }); postMock.mockResolvedValue({ data: pending });
			const { result, unmount } = renderHook(() => useStandaloneAgentReadiness("", true, true), { wrapper: wrapper(new QueryClient()) });
			await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
			act(() => result.current.retry());
			await act(async () => { await vi.advanceTimersByTimeAsync(0); });
			expect(postMock.mock.calls.filter(([path]) => path.endsWith("/probe"))).toEqual([
				["/api/v1/agents/{agent}/probe", { params: { path: { agent: "codex" } } }],
			]);
			unmount();
		} finally { vi.useRealTimers(); }
	});

	it.each(["ready", "signed out", "missing", "timeout"] as const)("keeps watching after a failed probe until %s", async (outcome) => {
		vi.useFakeTimers();
		try {
			const pending = { agents: [agentReadiness("codex", "Codex", { authentication: "unknown" })] };
			getMock.mockResolvedValue({ data: pending }); postMock.mockResolvedValue({ data: pending });
			const client = new QueryClient(); client.setQueryData(agentReadinessQueryKey, pending);
			const { result, unmount } = renderHook(() => {
				const agent = useAgentReadinessQuery().data?.agents[0];
				return useStandaloneAgentReadiness("codex", !agent || isPendingAgent(agent), true);
			}, { wrapper: wrapper(client) });
			await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
			const rejection = agentReadiness("codex", "Codex", { authentication: "unauthorized" });
			getMock.mockResolvedValue({ data: { agents: [rejection] } });
			postMock.mockRejectedValue(new Error("probe transport failed before invalidation"));
			act(() => result.current.retry());
			await act(async () => { await vi.advanceTimersByTimeAsync(0); });
			expect(result.current.retrying).toBe(true);
			expect(result.current.timedOut).toBe(false);
			act(() => { result.current.retry(); result.current.retry(); });
			getMock.mockResolvedValue({ data: { agents: [{ ...rejection, usageCount: 99 }, agentReadiness("claude-code")] } });
			await act(async () => { await vi.advanceTimersByTimeAsync(2_000); });
			expect(result.current.retrying).toBe(true);
			expect(postMock.mock.calls.filter(([path]) => path.endsWith("/probe"))).toHaveLength(1);
			if (outcome === "timeout") {
				await act(async () => { await vi.advanceTimersByTimeAsync(58_000); });
				expect(result.current.timedOut).toBe(true);
				expect(result.current.retrying).toBe(false);
			} else {
				const next = agentReadiness("codex", "Codex", outcome === "ready" ? {} : outcome === "missing" ? { installation: "not_installed" } : { authentication: "unauthorized" });
				if (outcome === "missing") next.installation.checkedAt = "2026-08-28T00:01:00Z";
				else next.authentication.checkedAt = "2026-08-28T00:01:00Z";
				getMock.mockResolvedValue({ data: { agents: [next] } });
				await act(async () => { await vi.advanceTimersByTimeAsync(2_000); });
				expect(result.current.retrying).toBe(false);
				expect(result.current.timedOut).toBe(false);
				expect(client.getQueryData(agentReadinessQueryKey)).toMatchObject({ agents: expect.arrayContaining([next]) });
			}
			const reads = getMock.mock.calls.length;
			await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
			expect(getMock).toHaveBeenCalledTimes(reads);
			unmount();
		} finally { vi.useRealTimers(); }
	});

	it.each(["unknown", "unauthorized"] as const)("probes installed %s candidates on Retry without a remembered agent", async (authentication) => {
		vi.useFakeTimers();
		try {
			const pending = { agents: [agentReadiness("codex", "Codex", { authentication }), agentReadiness("aider", "Aider", { installation: "not_installed" }), agentReadiness("claude-code", "Claude Code")] };
			getMock.mockResolvedValue({ data: pending }); postMock.mockResolvedValue({ data: pending });
			const { result, unmount } = renderHook(() => useStandaloneAgentReadiness("", true, true), { wrapper: wrapper(new QueryClient()) });
			await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
			act(() => result.current.retry());
			await act(async () => { await vi.advanceTimersByTimeAsync(0); });
			expect(postMock).toHaveBeenCalledWith("/api/v1/agents/{agent}/probe", { params: { path: { agent: "codex" } } });
			expect(postMock.mock.calls.filter(([path]) => path.endsWith("/probe"))).toHaveLength(1);
			unmount();
		} finally { vi.useRealTimers(); }
	});

	it("keeps a failed Retry inconclusive when its baseline read also failed", async () => {
		vi.useFakeTimers();
		try {
			const pending = { agents: [agentReadiness("codex", "Codex", { authentication: "unknown" })] };
			getMock.mockResolvedValue({ data: pending }); postMock.mockResolvedValue({ data: pending });
			const client = new QueryClient(); client.setQueryData(agentReadinessQueryKey, pending);
			const { result, unmount } = renderHook(() => {
				const agent = useAgentReadinessQuery().data?.agents[0];
				return useStandaloneAgentReadiness("codex", !agent || isPendingAgent(agent), true);
			}, { wrapper: wrapper(client) });
			await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
			const rejection = agentReadiness("codex", "Codex", { authentication: "unauthorized" });
			rejection.authentication.checkedAt = "2026-08-28T00:01:00Z";
			getMock.mockRejectedValueOnce(new Error("baseline unavailable")).mockResolvedValue({ data: { agents: [rejection] } });
			postMock.mockRejectedValue(new Error("probe transport failure"));
			act(() => result.current.retry());
			await act(async () => { await vi.advanceTimersByTimeAsync(2_000); });
			expect(result.current.retrying).toBe(true);
			expect(postMock).toHaveBeenLastCalledWith("/api/v1/agents/{agent}/probe", { params: { path: { agent: "codex" } } });
			getMock.mockResolvedValue({ data: { agents: [{ ...rejection, authentication: { ...rejection.authentication, freshness: "checking" } }] } });
			await act(async () => { await vi.advanceTimersByTimeAsync(2_000); });
			expect(result.current.retrying).toBe(true);
			getMock.mockResolvedValue({ data: { agents: [rejection] } });
			await act(async () => { await vi.advanceTimersByTimeAsync(2_000); });
			expect(result.current.retrying).toBe(false);
			expect(result.current.timedOut).toBe(false);
			unmount();
		} finally { vi.useRealTimers(); }
	});

	it("clears an expired unknown-installation Retry when that target is confirmed missing", async () => {
		vi.useFakeTimers();
		try {
			const pending = { agents: [agentReadiness("codex", "Codex", { installation: "unknown", authentication: "unknown" })] };
			getMock.mockResolvedValue({ data: pending }); postMock.mockResolvedValue({ data: pending });
			const client = new QueryClient(); client.setQueryData(agentReadinessQueryKey, pending);
			const { result, unmount } = renderHook(() => {
				const agents = useAgentReadinessQuery().data?.agents;
				return useStandaloneAgentReadiness("", !agents || agents.some(isPendingAgent), true);
			}, { wrapper: wrapper(client) });
			await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
			postMock.mockImplementation(() => new Promise(() => {}));
			act(() => result.current.retry());
			await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
			expect(result.current.timedOut).toBe(true);
			await act(async () => {
				client.setQueryData(agentReadinessQueryKey, { agents: [agentReadiness("codex", "Codex", { installation: "not_installed" })] });
				await vi.advanceTimersByTimeAsync(0);
			});
			expect(result.current.timedOut).toBe(false);
			unmount();
		} finally { vi.useRealTimers(); }
	});

	it.each([false, true])("finishes Retry despite an older cached rejection (transient read failure: %s)", async (readFails) => {
		vi.useFakeTimers();
		try {
			const pending = { agents: [agentReadiness("codex", "Codex", { authentication: "unknown" })] };
			getMock.mockResolvedValue({ data: pending }); postMock.mockResolvedValue({ data: pending });
			const client = new QueryClient(); client.setQueryData(agentReadinessQueryKey, pending);
			const { result, unmount } = renderHook(() => {
				const readiness = useAgentReadinessQuery();
				const agent = readiness.data?.agents[0];
				return useStandaloneAgentReadiness("codex", !agent || isPendingAgent(agent), true);
			}, { wrapper: wrapper(client) });
			await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
			let finish!: (response: unknown) => void;
			postMock.mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
			getMock.mockResolvedValue({ data: { agents: [agentReadiness("codex", "Codex", { authentication: "unauthorized" })] } });
			act(() => result.current.retry());
			await act(async () => {
				client.setQueryData(agentReadinessQueryKey, { agents: [agentReadiness("codex", "Codex", { authentication: "unauthorized" })] });
				await vi.advanceTimersByTimeAsync(0);
			});
			expect(result.current.retrying).toBe(true);
			getMock.mockResolvedValue({ data: { agents: [agentReadiness("codex", "Codex")] } });
			if (readFails) getMock.mockRejectedValueOnce(new Error("temporary transport failure"));
			await act(async () => { finish({ data: {} }); await vi.advanceTimersByTimeAsync(0); });
			if (readFails) {
				expect(result.current.retrying).toBe(true);
				await act(async () => { await vi.advanceTimersByTimeAsync(2_000); });
			}
			expect(client.getQueryData(agentReadinessQueryKey)).toMatchObject({ agents: [{ authentication: { state: "authorized" } }] });
			expect(result.current.retrying).toBe(false);
			unmount();
		} finally { vi.useRealTimers(); }
	});

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

	it.each(["another check", "Retry", "agent switch", "account switch"])("retains an expired Retry offer until settled by %s", async (action) => {
		vi.useFakeTimers();
		try {
			const pending = { agents: [agentReadiness("codex", "Codex", { authentication: "unknown" })] };
			getMock.mockResolvedValue({ data: pending }); postMock.mockResolvedValue({ data: pending });
			const client = new QueryClient(); client.setQueryData(agentReadinessQueryKey, pending);
			let selectedAgent = "codex";
			let selectedAccount: string | undefined;
			const { result, unmount, rerender } = renderHook(() => {
				const readiness = useAgentReadinessQuery();
				const agent = readiness.data?.agents.find((candidate) => candidate.id === selectedAgent);
				return useStandaloneAgentReadiness(selectedAgent, !selectedAccount && (!agent || isPendingAgent(agent)), true, selectedAccount);
			}, { wrapper: wrapper(client) });
			await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
			postMock.mockImplementation(() => new Promise(() => {}));
			act(() => result.current.retry());
			await act(async () => {
				client.setQueryData(agentReadinessQueryKey, { agents: [agentReadiness("codex", "Codex", { authentication: "unauthorized" })] });
				await vi.advanceTimersByTimeAsync(60_000);
			});
			expect(result.current.retrying).toBe(false);
			expect(result.current.timedOut).toBe(true);
			const reads = getMock.mock.calls.length;
			await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
			expect(getMock).toHaveBeenCalledTimes(reads);
			expect(result.current.timedOut).toBe(true);
			await act(async () => {
				const current = client.getQueryData<{ agents: ReturnType<typeof agentReadiness>[] }>(agentReadinessQueryKey)!;
				client.setQueryData(agentReadinessQueryKey, { agents: [...current.agents, agentReadiness("claude-code", "Claude Code")] });
				await vi.advanceTimersByTimeAsync(0);
			});
			expect(result.current.timedOut).toBe(true);
			// A later independent readiness result can settle the expired attempt.
			await act(async () => {
				const ready = { agents: [agentReadiness("codex", "Codex")] };
				if (action === "Retry") {
					getMock.mockResolvedValue({ data: ready });
					postMock.mockResolvedValue({ data: {} });
					result.current.retry();
				} else if (action === "agent switch") {
					client.setQueryData(agentReadinessQueryKey, { agents: [...pending.agents, agentReadiness("claude-code", "Claude Code")] });
					selectedAgent = "claude-code";
					rerender();
				} else if (action === "account switch") {
					selectedAccount = "signed-in-account";
					rerender();
				} else client.setQueryData(agentReadinessQueryKey, ready);
				await vi.advanceTimersByTimeAsync(0);
			});
			expect(result.current.timedOut).toBe(false);
			if (action === "Retry") expect(postMock).toHaveBeenCalledTimes(3);
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
			act(() => result.current.retry());
			await act(async () => { await vi.advanceTimersByTimeAsync(0); });
			expect(postMock).toHaveBeenCalledTimes(2);
			expect(postMock).toHaveBeenLastCalledWith("/api/v1/agents/{agent}/probe", { params: { path: { agent: "codex" } } });
			expect(result.current.timedOut).toBe(false);
			await act(async () => { finish({ data: {} }); });
			unmount();
		} finally { vi.useRealTimers(); }
	});

	it.each(["navigation", "daemon replacement"])("ignores a Retry result after %s", async (event) => {
		vi.useFakeTimers();
		try {
			const pending = { agents: [agentReadiness("codex", "Codex", { authentication: "unknown" })] };
			getMock.mockResolvedValue({ data: pending }); postMock.mockResolvedValue({ data: pending });
			const client = new QueryClient();
			const { result, unmount } = renderHook(() => useStandaloneAgentReadiness("codex", true, true), { wrapper: wrapper(client) });
			await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
			let finish!: (response: unknown) => void;
			postMock.mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
			act(() => result.current.retry());
			await act(async () => { await vi.advanceTimersByTimeAsync(0); });
			if (event === "navigation") unmount();
			else {
				postMock.mockResolvedValue({ data: pending });
				act(() => client.removeQueries({ queryKey: agentReadinessQueryKey, exact: true }));
				await act(async () => { await vi.advanceTimersByTimeAsync(0); });
				expect(result.current.retrying).toBe(false);
			}
			client.setQueryData(agentReadinessQueryKey, pending);
			const reads = getMock.mock.calls.length;
			getMock.mockResolvedValue({ data: { agents: [agentReadiness("codex", "Codex")] } });
			await act(async () => { finish({ data: {} }); await vi.advanceTimersByTimeAsync(0); });
			expect(getMock).toHaveBeenCalledTimes(reads);
			expect(client.getQueryData(agentReadinessQueryKey)).toEqual(pending);
			if (event !== "navigation") unmount();
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
