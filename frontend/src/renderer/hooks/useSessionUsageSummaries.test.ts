import { QueryClient } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";

const getMock = vi.hoisted(() => vi.fn());

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: (...args: unknown[]) => getMock(...args) },
}));

import { sessionUsageDetailQueryKey } from "./useSessionUsage";
import {
	fetchSessionUsageSummaries,
	sessionUsageQueryKey,
	sessionUsageQueryRoot,
	sessionUsageQueryOptions,
	warmSessionUsageSummaries,
} from "./useSessionUsageSummaries";

describe("session usage summaries", () => {
	beforeEach(() => {
		getMock.mockReset().mockResolvedValue({ data: { sessions: [] } });
	});

	it("fetches one project batch and relies on event invalidation", async () => {
		await fetchSessionUsageSummaries("reverb");

		expect(getMock).toHaveBeenCalledOnce();
		expect(getMock).toHaveBeenCalledWith("/api/v1/usage/sessions", {
			params: { query: { projectId: "reverb" } },
		});
		expect(sessionUsageQueryOptions("reverb")).not.toHaveProperty("refetchInterval");
	});

	it("warms each project's board cache without blocking the caller", async () => {
		const summary = {
			estimatedCost: null,
			incomplete: false,
			processedTokens: 42,
			sessionId: "reverb-61",
			totalTokens: 42,
		};
		getMock.mockResolvedValueOnce({ data: { sessions: [summary] } }).mockResolvedValueOnce({ data: { sessions: [] } });
		const queryClient = new QueryClient();

		expect(warmSessionUsageSummaries(queryClient, ["reverb", "landing"])).toBeUndefined();

		await vi.waitFor(() => expect(queryClient.getQueryData(sessionUsageQueryKey("landing"))).toEqual([]));
		expect(queryClient.getQueryData(sessionUsageQueryKey("reverb"))).toEqual([summary]);
		expect(getMock).toHaveBeenCalledWith("/api/v1/usage/sessions", { params: { query: { projectId: "reverb" } } });
		expect(getMock).toHaveBeenCalledWith("/api/v1/usage/sessions", { params: { query: { projectId: "landing" } } });
	});

	it("leaves an already cached board alone", async () => {
		const queryClient = new QueryClient();
		queryClient.setQueryData(sessionUsageQueryKey("reverb"), []);

		warmSessionUsageSummaries(queryClient, ["reverb"]);
		await Promise.resolve();

		expect(getMock).not.toHaveBeenCalled();
	});

	it("tries a failed warm-up once and leaves the board query to retry", async () => {
		getMock.mockRejectedValue(new Error("usage unavailable"));
		const queryClient = new QueryClient({ defaultOptions: { queries: { retryDelay: 0 } } });

		warmSessionUsageSummaries(queryClient, ["reverb"]);

		await vi.waitFor(() => expect(queryClient.getQueryState(sessionUsageQueryKey("reverb"))?.status).toBe("error"));
		expect(getMock).toHaveBeenCalledOnce();
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
