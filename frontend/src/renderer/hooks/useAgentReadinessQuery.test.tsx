import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { agentReadiness } from "../test/agent-readiness-fixtures";

const { getMock, postMock, remotePostMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn(),
	remotePostMock: vi.fn(),
}));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, POST: postMock },
	apiErrorMessage: () => "request failed",
}));

vi.mock("../lib/host-clients", () => ({
	clientForHost: () => ({ POST: remotePostMock }),
}));

import {
	agentReadinessQueryOptions,
	agentReadinessQueryKey,
	agentReadinessQueryKeyForHost,
	mergeAgentReadiness,
	useAgentReadinessQuery,
	useEnsureAgentReadiness,
	useEnsuredReadinessHosts,
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
	remotePostMock.mockReset().mockResolvedValue({
		data: { agents: [agentReadiness("claude-code", "Claude Code")] },
		error: undefined,
	});
});

describe("agent readiness query", () => {
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

describe("ensured readiness across hosts", () => {
	it("checks each host once, and a host joining never reopens another's gate", async () => {
		const queryClient = new QueryClient();
		const { result, rerender, unmount } = renderHook(
			({ hostIds }) => useEnsuredReadinessHosts(hostIds),
			{ initialProps: { hostIds: ["local"] }, wrapper: wrapper(queryClient) },
		);
		await waitFor(() => expect(result.current.has("local")).toBe(true));
		rerender({ hostIds: ["local", "host-vm"] });
		// This computer stays done while the remote's own check runs.
		expect(result.current.has("local")).toBe(true);
		await waitFor(() => expect(result.current.has("host-vm")).toBe(true));
		expect(postMock).toHaveBeenCalledTimes(1);
		expect(remotePostMock).toHaveBeenCalledTimes(1);
		expect(remotePostMock).toHaveBeenCalledWith("/api/v1/agents/readiness/ensure", { body: { agentIds: [], purpose: "display" } });
		expect(queryClient.getQueryData(agentReadinessQueryKeyForHost("host-vm"))).toEqual({ agents: [agentReadiness("claude-code", "Claude Code")] });
		unmount();
	});

	it("drops a host that leaves and checks it afresh when it returns", async () => {
		const { result, rerender, unmount } = renderHook(
			({ hostIds }) => useEnsuredReadinessHosts(hostIds),
			{ initialProps: { hostIds: ["local", "host-vm"] }, wrapper: wrapper(new QueryClient()) },
		);
		await waitFor(() => expect(result.current.has("host-vm")).toBe(true));
		rerender({ hostIds: ["local"] });
		expect(result.current.has("host-vm")).toBe(false);
		expect(result.current.has("local")).toBe(true);
		rerender({ hostIds: ["local", "host-vm"] });
		await waitFor(() => expect(result.current.has("host-vm")).toBe(true));
		expect(remotePostMock).toHaveBeenCalledTimes(2);
		expect(postMock).toHaveBeenCalledTimes(1);
		unmount();
	});

	it("keeps a remote that fails out of the gate without holding back this computer", async () => {
		remotePostMock.mockRejectedValue(new Error("offline"));
		const { result, unmount } = renderHook(() => useEnsuredReadinessHosts(["local", "host-vm"]), { wrapper: wrapper(new QueryClient()) });
		await waitFor(() => expect(result.current.has("local")).toBe(true));
		expect(result.current.has("host-vm")).toBe(false);
		unmount();
	});

	it("does not let a previous enabled period complete a newer check", async () => {
		const resolvers: Array<(value: unknown) => void> = [];
		postMock.mockImplementation(() => new Promise((resolve) => resolvers.push(resolve)));
		const { result, rerender, unmount } = renderHook(
			({ enabled }) => useEnsuredReadinessHosts(["local"], { enabled }),
			{ initialProps: { enabled: true }, wrapper: wrapper(new QueryClient()) },
		);
		rerender({ enabled: false });
		rerender({ enabled: true });
		const response = { data: { agents: [agentReadiness("codex")] } };
		await act(async () => resolvers[0](response));
		expect(result.current.has("local")).toBe(false);
		await act(async () => resolvers[1](response));
		expect(result.current.has("local")).toBe(true);
		rerender({ enabled: false });
		expect(result.current.size).toBe(0);
		unmount();
	});
});
