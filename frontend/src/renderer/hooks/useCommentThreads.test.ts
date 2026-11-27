import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { createElement, type ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useCommentThreadList, useUnreadCommentThreads } from "./useCommentThreads";

const client = vi.hoisted(() => ({ countMyUnreadCommentThreads: vi.fn(), listMyCommentThreads: vi.fn() }));
vi.mock("./useCloudCp", () => ({ useCloudCp: () => ({ ready: true, client, baseUrl: "https://cp.test", userId: "u-priya" }) }));
// Mentions and comment threads are offered unless a test says they are not.
const offered = vi.hoisted(() => ({ mentions: true }));
vi.mock("./useMentionsOffered", () => ({ useMentionsOffered: () => offered.mentions }));

function wrapper({ children }: { children: ReactNode }) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return createElement(QueryClientProvider, { client: queryClient }, children);
}

beforeEach(() => {
	offered.mentions = true;
	client.countMyUnreadCommentThreads.mockReset().mockResolvedValue({ count: 2 });
	client.listMyCommentThreads.mockReset().mockResolvedValue({ items: [], hasMore: false });
});

describe("the sidebar's Threads item (useUnreadCommentThreads)", () => {
	it("shows with its unread count while mentions are offered", async () => {
		const { result } = renderHook(() => useUnreadCommentThreads(), { wrapper });
		await waitFor(() => expect(result.current).toEqual({ count: 2, supported: true }));
	});

	it("is hidden, and reads nothing, while mentions are not offered", async () => {
		offered.mentions = false;
		const { result } = renderHook(() => useUnreadCommentThreads(), { wrapper });
		await act(async () => undefined);
		expect(result.current).toEqual({ count: 0, supported: false });
		expect(client.countMyUnreadCommentThreads).not.toHaveBeenCalled();
	});
});

describe("the threads list (useCommentThreadList)", () => {
	it("reads nothing while mentions are not offered", async () => {
		offered.mentions = false;
		renderHook(() => useCommentThreadList("open", false), { wrapper });
		await act(async () => undefined);
		expect(client.listMyCommentThreads).not.toHaveBeenCalled();
	});
});
