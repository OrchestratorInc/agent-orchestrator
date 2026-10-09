import { QueryClient } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";

const getMock = vi.hoisted(() => vi.fn());
const hostGetMock = vi.hoisted(() => vi.fn());
const clientForHostMock = vi.hoisted(() => vi.fn());

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: (...args: unknown[]) => getMock(...args) },
}));

vi.mock("../lib/host-clients", () => ({
	clientForHost: (hostId: string) => {
		clientForHostMock(hostId);
		return { GET: (...args: unknown[]) => hostGetMock(...args) };
	},
}));

import { sessionUsageDetailQueryKey } from "./useSessionUsage";
import {
	fetchSessionUsageSummaries,
	sessionUsageQueryKey,
	sessionUsageQueryRoot,
	sessionUsageQueryOptions,
	warmSessionUsageSummaries,
} from "./useSessionUsageSummaries";
import { STANDALONE_WORKSPACE_ID, type WorkspaceSummary } from "../types/workspace";

const usage = (sessionId: string, processedTokens: number) => ({
	estimatedCost: null,
	incomplete: false,
	processedTokens,
	sessionId,
	totalTokens: processedTokens,
});

const workspace = (id: string, sessionIds: string[], hostId?: string) =>
	({ id, hostId, name: id, sessions: sessionIds.map((sessionId) => ({ id: sessionId })) }) as unknown as WorkspaceSummary;

describe("session usage summaries", () => {
	beforeEach(() => {
		getMock.mockReset().mockResolvedValue({ data: { sessions: [] } });
		hostGetMock.mockReset();
		clientForHostMock.mockReset();
	});

	it("fetches one project batch and relies on event invalidation", async () => {
		await fetchSessionUsageSummaries("reverb");

		expect(getMock).toHaveBeenCalledOnce();
		expect(getMock).toHaveBeenCalledWith("/api/v1/usage/sessions", {
			params: { query: { projectId: "reverb" } },
		});
		expect(sessionUsageQueryOptions("reverb")).not.toHaveProperty("refetchInterval");
	});

	it("fills every board from one host-wide request", async () => {
		getMock.mockResolvedValueOnce({
			data: { sessions: [usage("reverb-1", 10), usage("reverb-2", 20), usage("landing-1", 30), usage("solo-1", 40)] },
		});
		const queryClient = new QueryClient();
		let now = 1_000;
		const clock = vi.spyOn(Date, "now").mockImplementation(() => (now += 1_000));

		await warmSessionUsageSummaries(queryClient, [
			workspace("reverb", ["reverb-1", "reverb-2"]),
			workspace("landing", ["landing-1", "landing-2"]),
			workspace("empty", []),
			workspace(STANDALONE_WORKSPACE_ID, ["solo-1"]),
		]);

		expect(getMock).toHaveBeenCalledOnce();
		expect(getMock).toHaveBeenCalledWith("/api/v1/usage/sessions", { params: { query: {} } });
		expect(queryClient.getQueryData(sessionUsageQueryKey("reverb"))).toEqual([usage("reverb-1", 10), usage("reverb-2", 20)]);
		expect(queryClient.getQueryData(sessionUsageQueryKey("landing"))).toEqual([usage("landing-1", 30)]);
		expect(queryClient.getQueryData(sessionUsageQueryKey("empty"))).toEqual([]);
		expect(queryClient.getQueryData(sessionUsageQueryKey(STANDALONE_WORKSPACE_ID))).toBeUndefined();
		// Seeded boards are as old as the response they came from, so a board
		// opened later still refetches once that response goes stale.
		expect(queryClient.getQueryState(sessionUsageQueryKey("reverb"))?.dataUpdatedAt).toBe(
			queryClient.getQueryState(sessionUsageQueryKey())?.dataUpdatedAt,
		);
		clock.mockRestore();
	});

	it("warms a remote host's boards from that host", async () => {
		hostGetMock.mockResolvedValueOnce({ data: { sessions: [usage("far-1", 5)] } });
		const queryClient = new QueryClient();

		await warmSessionUsageSummaries(queryClient, [workspace("far", ["far-1"], "host-b")]);

		expect(clientForHostMock).toHaveBeenCalledWith("host-b");
		expect(getMock).not.toHaveBeenCalled();
		expect(queryClient.getQueryData(sessionUsageQueryKey("far", "host-b"))).toEqual([usage("far-1", 5)]);
		expect(queryClient.getQueryData(sessionUsageQueryKey("far"))).toBeUndefined();
	});

	it("does not request usage when every board is already cached", async () => {
		const queryClient = new QueryClient();
		queryClient.setQueryData(sessionUsageQueryKey("reverb"), []);

		await warmSessionUsageSummaries(queryClient, [workspace("reverb", ["reverb-1"])]);

		expect(getMock).not.toHaveBeenCalled();
	});

	it("keeps data a board loaded while the warm-up was in flight", async () => {
		let resolve!: (value: unknown) => void;
		getMock.mockReturnValueOnce(new Promise((next) => { resolve = next; }));
		const queryClient = new QueryClient();

		const warming = warmSessionUsageSummaries(queryClient, [workspace("reverb", ["reverb-1"])]);
		queryClient.setQueryData(sessionUsageQueryKey("reverb"), [usage("reverb-1", 99)]);
		resolve({ data: { sessions: [usage("reverb-1", 1)] } });
		await warming;

		expect(queryClient.getQueryData(sessionUsageQueryKey("reverb"))).toEqual([usage("reverb-1", 99)]);
	});

	it("tries a failed warm-up once and leaves boards to fetch for themselves", async () => {
		getMock.mockRejectedValue(new Error("usage unavailable"));
		const queryClient = new QueryClient({ defaultOptions: { queries: { retryDelay: 0 } } });

		await expect(warmSessionUsageSummaries(queryClient, [workspace("reverb", ["reverb-1"])])).resolves.toBeUndefined();

		expect(getMock).toHaveBeenCalledOnce();
		expect(queryClient.getQueryData(sessionUsageQueryKey("reverb"))).toBeUndefined();
		expect(sessionUsageQueryOptions("reverb").retry).toBe(1);
	});

	it("keeps summaries cached after the board unmounts", () => {
		expect(sessionUsageQueryOptions("reverb").gcTime).toBe(Number.POSITIVE_INFINITY);
	});

	// The detail query lives in useSessionUsage.ts and must stay beneath this
	// root, or a usage event invalidates the board summaries without touching
	// the inspector's open session.
	it("keeps the detail query beneath the shared usage query root", () => {
		expect(sessionUsageDetailQueryKey("sess-1")).toEqual([
			...sessionUsageQueryRoot,
			"detail",
			"sess-1",
		]);
	});
});
