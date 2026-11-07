import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CloudCpError } from "../lib/cloud-cp/errors";
import { resetTeamCollabSupport, teamCollabSupport } from "../lib/cloud-team-collab";
import { teamFeedQueryKey, useMarkFeedSeen, useMentionCandidates, useTeamCollabSupport, useTeamFeed } from "./useTeamCollab";

const mocks = vi.hoisted(() => ({
	ready: true,
	getMyFeed: vi.fn(),
	markFeedSeen: vi.fn(),
	listMentionCandidates: vi.fn(),
}));
vi.mock("./useCloudCp", () => ({
	useCloudCp: () => ({ ready: mocks.ready, client: mocks, baseUrl: "https://cp.test", userId: "user-me" }),
}));

let client: QueryClient;
function wrapper({ children }: { children: ReactNode }) {
	return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

beforeEach(() => {
	vi.clearAllMocks();
	mocks.ready = true;
	resetTeamCollabSupport();
	client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	mocks.getMyFeed.mockResolvedValue({ items: [], page: { hasMore: false } });
	mocks.markFeedSeen.mockResolvedValue(undefined);
	mocks.listMentionCandidates.mockResolvedValue({ people: [], teams: [] });
});
afterEach(() => resetTeamCollabSupport());

describe("useTeamCollabSupport", () => {
	it("asks once with a one-row page of the caller's own feed, and remembers yes", async () => {
		const { result } = renderHook(() => useTeamCollabSupport(), { wrapper });
		expect(result.current).toBe("unknown");
		await waitFor(() => expect(result.current).toBe("supported"));
		expect(mocks.getMyFeed).toHaveBeenCalledTimes(1);
		expect(mocks.getMyFeed).toHaveBeenCalledWith({ scope: "mine", limit: 1 });
	});

	it("remembers no on a control plane without the feed", async () => {
		mocks.getMyFeed.mockRejectedValue(new CloudCpError("Not found", { status: 404 }));
		const { result } = renderHook(() => useTeamCollabSupport(), { wrapper });
		await waitFor(() => expect(result.current).toBe("unsupported"));
	});

	it("stays undecided while the control plane errs another way", async () => {
		mocks.getMyFeed.mockRejectedValue(new CloudCpError("Boom", { status: 503, code: "unavailable" }));
		const { result } = renderHook(() => useTeamCollabSupport(), { wrapper });
		await waitFor(() => expect(mocks.getMyFeed).toHaveBeenCalled());
		await act(async () => undefined);
		expect(result.current).toBe("unknown");
	});

	it("asks nothing while signed out", async () => {
		mocks.ready = false;
		const { result } = renderHook(() => useTeamCollabSupport(), { wrapper });
		await act(async () => undefined);
		expect(result.current).toBe("unknown");
		expect(mocks.getMyFeed).not.toHaveBeenCalled();
	});
});

describe("useTeamFeed and useMarkFeedSeen", () => {
	it("pages by cursor and learns support from the answer", async () => {
		mocks.getMyFeed.mockImplementation(async (query: { cursor?: string }) => query.cursor
			? { items: [{ sessionId: "b" }], page: { hasMore: false } }
			: { items: [{ sessionId: "a" }], page: { hasMore: true, nextCursor: "c1" }, away: { needsYou: 1 } });
		const { result } = renderHook(() => useTeamFeed("following"), { wrapper });
		await waitFor(() => expect(result.current.hasNextPage).toBe(true));
		expect(teamCollabSupport("https://cp.test")).toBe("supported");
		await act(async () => { await result.current.fetchNextPage(); });
		await waitFor(() => expect(result.current.data?.pages.flatMap((page) => page.items.map((item) => item.sessionId))).toEqual(["a", "b"]));
		expect(mocks.getMyFeed).toHaveBeenLastCalledWith({ scope: "following", limit: 30, cursor: "c1" }, expect.anything());
		expect(result.current.hasNextPage).toBe(false);
	});

	it("drops every tab's While you were away once the visit is recorded", async () => {
		mocks.getMyFeed.mockResolvedValue({ items: [], page: { hasMore: false }, away: { since: "", needsYou: 1, prsMerged: [], failed: [], started: { count: 0, names: [] } } });
		const feed = renderHook(() => useTeamFeed("team"), { wrapper });
		await waitFor(() => expect(feed.result.current.data?.pages[0]?.away).toBeDefined());
		const seen = renderHook(() => useMarkFeedSeen(), { wrapper });
		await act(async () => { await seen.result.current.mutateAsync(); });
		expect(mocks.markFeedSeen).toHaveBeenCalledTimes(1);
		const cached = client.getQueryData<{ pages: Array<{ away?: unknown }> }>(teamFeedQueryKey("https://cp.test", "user-me", "team"));
		expect(cached?.pages[0]?.away).toBeUndefined();
	});
});

describe("useMentionCandidates", () => {
	it("asks once typing settles, and not at all when off", async () => {
		const { rerender } = renderHook(({ query, enabled }) => useMentionCandidates("org-1", "session-1", query, enabled), {
			wrapper, initialProps: { query: undefined as string | undefined, enabled: true },
		});
		await act(async () => undefined);
		expect(mocks.listMentionCandidates).not.toHaveBeenCalled();
		rerender({ query: "ch", enabled: true });
		await waitFor(() => expect(mocks.listMentionCandidates).toHaveBeenCalledWith("org-1", "session-1", { q: "ch", limit: 20 }, expect.anything()));
		rerender({ query: "chen", enabled: false });
		await new Promise((resolve) => setTimeout(resolve, 200));
		expect(mocks.listMentionCandidates).toHaveBeenCalledTimes(1);
	});
});
