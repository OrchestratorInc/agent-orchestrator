import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));

// The local daemon's client is mocked, not host resolution: a search on this
// machine must go through clientForSessionHost's local path (clientForHost
// throws for the local host), and only an unmocked host-clients proves it.
vi.mock("../lib/api-client", async (importOriginal) => ({
	...await importOriginal<typeof import("../lib/api-client")>(),
	apiClient: { GET: getMock },
	apiErrorMessage: () => "search failed",
}));

import { SESSION_SEARCH_MIN_QUERY_CHARS, useSessionSearch } from "./useSessionSearch";

function wrapper({ children }: { children: ReactNode }) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

const emptyFilters = { query: "", projectId: "", harness: "", from: "", to: "" };

describe("useSessionSearch", () => {
	beforeEach(() => {
		getMock.mockReset();
	});

	it("does not ask the daemon until the query is long enough", () => {
		const { result } = renderHook(
			() => useSessionSearch({ ...emptyFilters, query: "a" }),
			{ wrapper },
		);
		expect(result.current.fetchStatus).toBe("idle");
		expect(getMock).not.toHaveBeenCalled();
		expect(SESSION_SEARCH_MIN_QUERY_CHARS).toBeGreaterThan(1);
	});

	it("sends the query and every active filter", async () => {
		getMock.mockResolvedValue({
			data: {
				query: "flaky login",
				results: [],
				total: 0,
				limit: 25,
				offset: 0,
				coverage: { state: "ready" },
			},
		});
		const { result } = renderHook(
			() => useSessionSearch({
				query: "  flaky login  ",
				projectId: "proj",
				harness: "claude-code",
				from: "2026-01-01",
				to: "2026-06-01",
			}),
			{ wrapper },
		);
		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(getMock).toHaveBeenCalledWith("/api/v1/search/sessions", {
			params: {
				query: {
					q: "flaky login",
					projectId: "proj",
					harness: "claude-code",
					from: "2026-01-01",
					to: "2026-06-01",
					limit: 25,
				},
			},
		});
	});

	it("omits filters the caller left empty", async () => {
		getMock.mockResolvedValue({
			data: { query: "reaper", results: [], total: 0, limit: 25, offset: 0, coverage: { state: "ready" } },
		});
		const { result } = renderHook(
			() => useSessionSearch({ ...emptyFilters, query: "reaper" }),
			{ wrapper },
		);
		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(getMock).toHaveBeenCalledWith("/api/v1/search/sessions", {
			params: { query: { q: "reaper", limit: 25 } },
		});
	});

	it("surfaces a daemon error instead of an empty result", async () => {
		getMock.mockResolvedValue({ error: { message: "boom" } });
		const { result } = renderHook(
			() => useSessionSearch({ ...emptyFilters, query: "reaper" }),
			{ wrapper },
		);
		// The hook retries once, so the error state arrives after both attempts.
		await waitFor(() => expect(result.current.isError).toBe(true), { timeout: 5_000 });
		expect(result.current.error?.message).toBe("search failed");
	});
});