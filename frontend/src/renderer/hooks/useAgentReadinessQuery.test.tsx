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
