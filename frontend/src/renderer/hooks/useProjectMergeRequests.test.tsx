import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { useProjectMergeRequests } from "./useProjectMergeRequests";

const mocks = vi.hoisted(() => ({
	version: undefined as string | undefined,
	liveFor: vi.fn(),
	list: vi.fn(async () => ({ mergeRequests: [] })),
}));
vi.mock("./useCloudCp", () => ({ useCloudCp: () => ({ client: { listProjectMergeRequests: mocks.list }, baseUrl: "https://cp.test", ready: true }) }));
vi.mock("./useCloudSessionLive", () => ({
	useCloudSessionLive: (_org: string, sessionId: string | undefined, enabled: boolean) => {
		mocks.liveFor(sessionId, enabled);
		return mocks.version === undefined ? undefined : { mergeRequestsVersion: mocks.version };
	},
}));

describe("useProjectMergeRequests", () => {
	it("re-reads the moment the session's live state says a request changed", async () => {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
		const { rerender } = renderHook(() => useProjectMergeRequests("org-1", "project-1", true, "orchestrator-1"), { wrapper });
		await waitFor(() => expect(mocks.list).toHaveBeenCalledTimes(1));
		expect(mocks.liveFor).toHaveBeenLastCalledWith("orchestrator-1", true);

		mocks.version = "2026-10-26 10:00:00+00/1";
		rerender();
		await waitFor(() => expect(mocks.list).toHaveBeenCalledTimes(2));

		rerender();
		await new Promise((resolve) => setTimeout(resolve, 20));
		expect(mocks.list).toHaveBeenCalledTimes(2);

		mocks.version = "2026-10-26 10:01:00+00/1";
		rerender();
		await waitFor(() => expect(mocks.list).toHaveBeenCalledTimes(3));
	});
});
