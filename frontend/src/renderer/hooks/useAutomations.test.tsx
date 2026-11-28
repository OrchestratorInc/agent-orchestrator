import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useAutomations, useCreateAutomation } from "./useAutomations";

const { getMock, postMock } = vi.hoisted(() => ({ getMock: vi.fn(), postMock: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, POST: postMock },
	apiErrorMessage: () => "request failed",
}));

function wrapper(queryClient: QueryClient) {
	return ({ children }: { children: ReactNode }) => (
		<QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
	);
}

describe("useAutomations", () => {
	beforeEach(() => {
		getMock.mockReset();
		postMock.mockReset();
	});

	it("fetches every automation page", async () => {
		getMock
			.mockResolvedValueOnce({ data: { automations: [{ id: "automation-1" }], nextCursor: "cursor-2" }, error: undefined })
			.mockResolvedValueOnce({ data: { automations: [{ id: "automation-2" }] }, error: undefined });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });

		const { result } = renderHook(() => useAutomations(), { wrapper: wrapper(queryClient) });

		await waitFor(() => expect(result.current.data?.map((item) => item.id)).toEqual(["automation-1", "automation-2"]));
		expect(getMock).toHaveBeenNthCalledWith(1, "/api/v1/automations", { params: { query: { limit: 100, cursor: undefined } } });
		expect(getMock).toHaveBeenNthCalledWith(2, "/api/v1/automations", { params: { query: { limit: 100, cursor: "cursor-2" } } });
	});

	it("names the daemon's project-less automations by the Scratchpad's id", async () => {
		getMock.mockResolvedValueOnce({ data: { automations: [{ id: "scratch", projectId: "" }, { id: "project", projectId: "demo" }] }, error: undefined });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });

		const { result } = renderHook(() => useAutomations(), { wrapper: wrapper(queryClient) });

		await waitFor(() => expect(result.current.data?.map((item) => item.projectId)).toEqual(["__standalone__", "demo"]));
	});

	it("creates a Scratchpad automation with no project and lists it by the Scratchpad's id", async () => {
		postMock.mockResolvedValue({ data: { automation: { id: "scratch", projectId: "" } }, error: undefined });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const { result } = renderHook(() => useCreateAutomation(), { wrapper: wrapper(queryClient) });

		let created: { projectId: string } | undefined;
		await act(async () => {
			created = await result.current.mutateAsync({ projectId: "__standalone__", displayName: "Digest", prompt: "Summarize", kind: "worker", timezone: "UTC", harness: "codex" });
		});

		expect(postMock).toHaveBeenCalledWith("/api/v1/automations", { body: expect.objectContaining({ projectId: "", harness: "codex" }) });
		expect(created?.projectId).toBe("__standalone__");
	});
});
