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

import {
	usePaletteSessionSearch,
	useSessionSearchIndexStatus,
	useSessionSearchMatches,
} from "./usePaletteSessionSearch";

function wrapper({ children }: { children: ReactNode }) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

describe("usePaletteSessionSearch", () => {
	beforeEach(() => {
		getMock.mockReset();
	});

	it("stays idle below two characters", () => {
		const { result } = renderHook(() => usePaletteSessionSearch("f"), { wrapper });
		expect(result.current.fetchStatus).toBe("idle");
		expect(getMock).not.toHaveBeenCalled();
	});

	it("asks for a short page so the palette stays a launcher", async () => {
		getMock.mockResolvedValue({ data: { query: "flaky", results: [], total: 0, limit: 8, offset: 0, sort: "relevance", coverage: { state: "ready" } } });
		const { result } = renderHook(() => usePaletteSessionSearch("  flaky  "), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(getMock).toHaveBeenCalledWith("/api/v1/search/sessions", {
			params: { query: { q: "flaky", limit: 8 } },
		});
	});

	it("does not retry a failing search", async () => {
		getMock.mockResolvedValue({ error: { message: "boom" } });
		const { result } = renderHook(() => usePaletteSessionSearch("flaky"), { wrapper });
		await waitFor(() => expect(result.current.isError).toBe(true));
		expect(getMock).toHaveBeenCalledTimes(1);
		expect(result.current.error?.message).toBe("search failed");
	});

	it("does nothing while disabled", () => {
		const { result } = renderHook(() => usePaletteSessionSearch("flaky", undefined, false), { wrapper });
		expect(result.current.fetchStatus).toBe("idle");
		expect(getMock).not.toHaveBeenCalled();
	});
});

describe("useSessionSearchIndexStatus", () => {
	beforeEach(() => {
		getMock.mockReset();
	});

	it("reports coverage for the status line", async () => {
		getMock.mockResolvedValue({ data: { coverage: { state: "backfilling", pendingSources: 3 } } });
		const { result } = renderHook(() => useSessionSearchIndexStatus(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(result.current.data).toEqual({ state: "backfilling", pendingSources: 3 });
	});

	// A missing status must not become a visible error in the palette: the
	// search itself is the thing the person asked for.
	it("swallows an unavailable status", async () => {
		getMock.mockResolvedValue({ error: { message: "unavailable" } });
		const { result } = renderHook(() => useSessionSearchIndexStatus(), { wrapper });
		await waitFor(() => expect(result.current.isFetching).toBe(false));
		expect(result.current.isError).toBe(false);
		expect(result.current.data).toBeNull();
	});
});

describe("useSessionSearchMatches", () => {
	beforeEach(() => {
		getMock.mockReset();
	});

	it("lists the matches for one session so Enter can step through them", async () => {
		getMock.mockResolvedValue({
			data: {
				matches: [
					{ sessionId: "proj-1", projectId: "proj", kind: "user", messageId: "msg-1", seq: 1, snippet: "retry" },
					{ sessionId: "proj-1", projectId: "proj", kind: "assistant", messageId: "msg-2", seq: 2, snippet: "retry" },
				],
			},
		});
		const { result } = renderHook(() => useSessionSearchMatches("proj-1", "retry"), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(result.current.data).toHaveLength(2);
		expect(getMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/search/matches", {
			params: { path: { sessionId: "proj-1" }, query: { q: "retry" } },
		});
	});

	it("stays idle with no session or no query", () => {
		const { result } = renderHook(() => useSessionSearchMatches(undefined, "retry"), { wrapper });
		expect(result.current.fetchStatus).toBe("idle");
		const empty = renderHook(() => useSessionSearchMatches("proj-1", "  "), { wrapper });
		expect(empty.result.current.fetchStatus).toBe("idle");
		expect(getMock).not.toHaveBeenCalled();
	});
});