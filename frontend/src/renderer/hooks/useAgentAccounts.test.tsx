import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
	agentAccountsQueryKey,
	fetchAgentAccounts,
	resetFreshAgentAccountsReadForTests,
	useAgentAccountActions,
	useFreshAgentAccountsRead,
} from "./useAgentAccounts";

const { getMock, postMock } = vi.hoisted(() => ({ getMock: vi.fn(), postMock: vi.fn() }));

vi.mock("../lib/api-client", async (importOriginal) => ({
	...(await importOriginal<typeof import("../lib/api-client")>()),
	apiClient: { GET: getMock, POST: postMock },
}));

function renderRead() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
	return { client, ...renderHook(() => ({ fresh: useFreshAgentAccountsRead(), actions: useAgentAccountActions() }), { wrapper }) };
}

describe("useFreshAgentAccountsRead", () => {
	beforeEach(() => {
		getMock.mockReset();
		postMock.mockReset();
		getMock.mockResolvedValue({ data: { accounts: [{ id: "acct-1" }] } });
		resetFreshAgentAccountsReadForTests();
		vi.spyOn(Date, "now").mockReturnValue(1_000_000);
	});
	afterEach(() => vi.restoreAllMocks());

	it("reads past the daemon cache at most once a minute", async () => {
		const { client, result } = renderRead();
		act(() => {
			result.current.fresh.read();
			result.current.fresh.read();
		});
		expect(getMock).toHaveBeenCalledTimes(1);
		expect(getMock).toHaveBeenCalledWith("/api/v1/agents/accounts", { params: { query: { refresh: true } } });
		await waitFor(() => expect(client.getQueryData(agentAccountsQueryKey)).toEqual([{ id: "acct-1" }]));

		vi.mocked(Date.now).mockReturnValue(1_059_999);
		act(() => result.current.fresh.read());
		expect(getMock).toHaveBeenCalledTimes(1);
		vi.mocked(Date.now).mockReturnValue(1_060_000);
		act(() => result.current.fresh.read());
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
		act(() => result.current.fresh.read());
		await waitFor(() => expect(client.getQueryData(agentAccountsQueryKey)).toEqual([{ id: "fresh" }]));
		resolvePoll?.({ data: { accounts: [{ id: "stale" }] } });
		await poll;
		expect(client.getQueryData(agentAccountsQueryKey)).toEqual([{ id: "fresh" }]);
	});

	it("keeps an account change made while the fresh read was out", async () => {
		let resolveRead: ((value: unknown) => void) | undefined;
		getMock.mockImplementationOnce(() => new Promise((resolve) => { resolveRead = resolve; }));
		postMock.mockResolvedValue({ data: { accounts: [{ id: "acct-2", default: true }] } });
		const { client, result } = renderRead();
		const invalidate = vi.spyOn(client, "invalidateQueries");
		act(() => result.current.fresh.read());
		await act(() => result.current.actions.makeDefault("acct-2"));
		resolveRead?.({ data: { accounts: [{ id: "acct-1" }, { id: "acct-2" }] } });
		await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: agentAccountsQueryKey, exact: true }));
		expect(client.getQueryData(agentAccountsQueryKey)).toEqual([{ id: "acct-2", default: true }]);
	});

	it("ignores a read overtaken by a newer one", async () => {
		let rejectFirst: ((reason: unknown) => void) | undefined;
		getMock.mockImplementationOnce(() => new Promise((_resolve, reject) => { rejectFirst = reject; }));
		getMock.mockResolvedValueOnce({ data: { accounts: [{ id: "newer" }] } });
		const { client, result } = renderRead();
		act(() => result.current.fresh.read());
		vi.mocked(Date.now).mockReturnValue(1_060_000);
		act(() => result.current.fresh.read());
		await waitFor(() => expect(client.getQueryData(agentAccountsQueryKey)).toEqual([{ id: "newer" }]));
		await act(async () => { rejectFirst?.(new Error("timed out")); });
		expect(result.current.fresh.error).toBeNull();
		// The throttle still counts the newer read, so no extra read starts.
		act(() => result.current.fresh.read());
		expect(getMock).toHaveBeenCalledTimes(2);
	});

	it("reports a failed read and tries again on the next call", async () => {
		getMock.mockRejectedValueOnce(new Error("daemon down"));
		const { client, result } = renderRead();
		act(() => result.current.fresh.read());
		await waitFor(() => expect(result.current.fresh.error?.message).toBe("daemon down"));
		expect(client.getQueryData(agentAccountsQueryKey)).toBeUndefined();

		act(() => result.current.fresh.read());
		expect(getMock).toHaveBeenCalledTimes(2);
		await waitFor(() => expect(result.current.fresh.error).toBeNull());
		expect(client.getQueryData(agentAccountsQueryKey)).toEqual([{ id: "acct-1" }]);
	});
});
