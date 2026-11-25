import { infiniteQueryOptions, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { conversationQueryKey } from "./useConversation";
import { useSwitchHistoryRead } from "./useSwitchHistoryRead";
import { workspaceQueryKey } from "./useWorkspaceQuery";

const { reads } = vi.hoisted(() => ({ reads: new Map<string, (page: unknown) => void>() }));

vi.mock("../lib/api-client", async (importOriginal) => ({
	...await importOriginal<typeof import("../lib/api-client")>(),
	hasTrustedApiBaseUrl: () => true,
}));

vi.mock("./useConversation", async (importOriginal) => {
	const actual = await importOriginal<typeof import("./useConversation")>();
	return {
		...actual,
		conversationQueryOptions: (sessionId: string) => infiniteQueryOptions({
			queryKey: actual.conversationQueryKey(sessionId),
			initialPageParam: undefined as number | undefined,
			queryFn: () => new Promise((resolve) => reads.set(sessionId, resolve)),
			getNextPageParam: () => undefined,
			retry: false,
		}),
	};
});

let queryClient: QueryClient;

function wrapper({ children }: { children: ReactNode }) {
	return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

beforeEach(() => {
	reads.clear();
	queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	queryClient.setQueryData(workspaceQueryKey, [{
		id: "p1",
		name: "Project",
		sessions: [
			{ id: "chat", mode: "chat" },
			{ id: "terminal", mode: "tui" },
			{ id: "cloud", mode: "chat", cloud: { orgId: "o1" } },
			{ id: "imported", mode: "chat", needsResume: true },
		],
	}]);
});

describe("useSwitchHistoryRead", () => {
	it("starts reading an uncached local chat as the switch begins, and reports it until it lands", async () => {
		const hook = renderHook(({ sessionId }) => useSwitchHistoryRead(sessionId), { initialProps: { sessionId: "chat" }, wrapper });

		expect(reads.has("chat")).toBe(true);
		expect(hook.result.current).toBe(true);

		await act(async () => reads.get("chat")?.({ items: [] }));

		expect(hook.result.current).toBe(false);
		expect(queryClient.getQueryData(conversationQueryKey("chat"))).toBeDefined();
	});

	it("reads nothing for a terminal, Cloud or imported session, or a chat already cached", () => {
		queryClient.setQueryData(conversationQueryKey("cached"), { pages: [{}], pageParams: [undefined] });
		for (const sessionId of ["terminal", "cloud", "imported", "cached"]) {
			const hook = renderHook(() => useSwitchHistoryRead(sessionId), { wrapper });
			expect(hook.result.current).toBe(false);
			hook.unmount();
		}
		expect(reads.size).toBe(0);
	});

	it("reports a first read someone else started, such as the sidebar's hover prefetch", () => {
		queryClient.setQueryData(workspaceQueryKey, []);
		void queryClient.prefetchInfiniteQuery({
			queryKey: conversationQueryKey("elsewhere"),
			initialPageParam: undefined,
			queryFn: () => new Promise(() => {}),
			getNextPageParam: () => undefined,
		});

		expect(renderHook(() => useSwitchHistoryRead("elsewhere"), { wrapper }).result.current).toBe(true);
	});
});
