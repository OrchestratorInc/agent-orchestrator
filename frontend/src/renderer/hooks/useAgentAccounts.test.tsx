import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { agentAccountsQueryKey, resetFreshAgentAccountsReadForTests, useFreshAgentAccountsRead } from "./useAgentAccounts";

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

	it("leaves a failed read for the next poll to surface", async () => {
		getMock.mockRejectedValue(new Error("daemon down"));
		const { client, result } = renderRead();
		result.current();
		await waitFor(() => expect(getMock).toHaveBeenCalledTimes(1));
		await Promise.resolve();
		expect(client.getQueryData(agentAccountsQueryKey)).toBeUndefined();
	});
});
