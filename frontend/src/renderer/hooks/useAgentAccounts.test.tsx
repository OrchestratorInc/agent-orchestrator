import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { agentAccountsQueryKey, fetchAgentAccounts, resetFreshAgentAccountsReadForTests, useFreshAgentAccountsRead } from "./useAgentAccounts";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));

vi.mock("../lib/api-client", async (importOriginal) => ({
	...(await importOriginal<typeof import("../lib/api-client")>()),
	apiClient: { GET: getMock },
}));

function renderRead() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
	return { client, ...renderHook(() => useFreshAgentAccountsRead(), { wrapper }) };
}

describe("useFreshAgentAccountsRead", () => {
	beforeEach(() => {
		getMock.mockReset();
		getMock.mockResolvedValue({ data: { accounts: [{ id: "acct-1" }] } });
		resetFreshAgentAccountsReadForTests();
		vi.spyOn(Date, "now").mockReturnValue(1_000_000);
	});
	afterEach(() => vi.restoreAllMocks());

	it("reads past the daemon cache at most once a minute", async () => {
		const { client, result } = renderRead();
		result.current();
		result.current();
		expect(getMock).toHaveBeenCalledTimes(1);
		expect(getMock).toHaveBeenCalledWith("/api/v1/agents/accounts", { params: { query: { refresh: true } } });
		await waitFor(() => expect(client.getQueryData(agentAccountsQueryKey)).toEqual([{ id: "acct-1" }]));

		vi.mocked(Date.now).mockReturnValue(1_059_999);
		result.current();
		expect(getMock).toHaveBeenCalledTimes(1);
		vi.mocked(Date.now).mockReturnValue(1_060_000);
		result.current();
		expect(getMock).toHaveBeenCalledTimes(2);
	});

	it("drops an older poll that lands after the fresh read", async () => {
		let resolvePoll: ((value: unknown) => void) | undefined;
		getMock.mockImplementation((_path: string, { params }: { params: { query: { refresh?: boolean } } }) =>
			params.query.refresh
				? Promise.resolve({ data: { accounts: [{ id: "fresh" }] } })
				: new Promise((resolve) => { resolvePoll = resolve; }));
		const { client, result } = renderRead();
		const poll = client.fetchQuery({ queryKey: agentAccountsQueryKey, queryFn: () => fetchAgentAccounts() }).catch(() => undefined);
		result.current();
		await waitFor(() => expect(client.getQueryData(agentAccountsQueryKey)).toEqual([{ id: "fresh" }]));
		resolvePoll?.({ data: { accounts: [{ id: "stale" }] } });
		await poll;
		expect(client.getQueryData(agentAccountsQueryKey)).toEqual([{ id: "fresh" }]);
	});

	it("leaves a failed read for the next poll to surface", async () => {
		getMock.mockRejectedValue(new Error("daemon down"));
		const { client, result } = renderRead();
		result.current();
		await waitFor(() => expect(getMock).toHaveBeenCalledTimes(1));
		await Promise.resolve();
		expect(client.getQueryData(agentAccountsQueryKey)).toBeUndefined();
	});
});
