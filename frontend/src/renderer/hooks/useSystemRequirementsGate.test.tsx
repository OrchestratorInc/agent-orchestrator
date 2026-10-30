import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

const apiBaseUrlState = vi.hoisted(() => ({
	url: "",
	listeners: new Set<() => void>(),
}));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: vi.fn(), POST: vi.fn() },
	apiErrorMessage: (error: unknown) => String(error),
	getApiBaseUrl: () => apiBaseUrlState.url,
	subscribeApiBaseUrl: (listener: () => void) => {
		apiBaseUrlState.listeners.add(listener);
		return () => apiBaseUrlState.listeners.delete(listener);
	},
}));

vi.mock("../lib/preview-mode", () => ({ usesPreviewWorkspaceData: false }));

import { apiClient } from "../lib/api-client";
import { systemRequirementsQueryKey, useSystemRequirementsGate } from "./useSystemRequirementsGate";

function wrapper(queryClient: QueryClient) {
	return function Wrapper({ children }: { children: ReactNode }) {
		return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
	};
}

function setBaseUrl(url: string) {
	act(() => {
		apiBaseUrlState.url = url;
		apiBaseUrlState.listeners.forEach((listener) => listener());
	});
}

afterEach(() => {
	vi.useRealTimers();
	apiBaseUrlState.url = "";
	apiBaseUrlState.listeners.clear();
	vi.mocked(apiClient.GET).mockReset();
});

describe("useSystemRequirementsGate", () => {
	it("waits for a trusted daemon URL and checks fresh requirements after a restart", async () => {
		const getMock = vi.mocked(apiClient.GET);
		getMock.mockResolvedValueOnce({ data: { requirements: [] }, error: undefined } as never);
		const deferred: { resolve: (value: unknown) => void } = { resolve: () => undefined };
		getMock.mockReturnValueOnce(new Promise<unknown>((resolve) => {
			deferred.resolve = resolve;
		}) as never);
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const { result } = renderHook(() => useSystemRequirementsGate(), { wrapper: wrapper(queryClient) });

		await act(async () => Promise.resolve());
		expect(getMock).not.toHaveBeenCalled();
		expect(result.current.blocked).toBe(true);

		setBaseUrl("http://127.0.0.1:3037");
		await waitFor(() => expect(result.current.ready).toBe(true));
		expect(getMock).toHaveBeenCalledTimes(1);

		setBaseUrl("");
		setBaseUrl("http://127.0.0.1:4555");
		await waitFor(() => expect(getMock).toHaveBeenCalledTimes(2));
		expect(result.current.checking).toBe(true);
		expect(result.current.blocked).toBe(true);

		act(() => deferred.resolve({ data: { requirements: [{ required: true, satisfied: false }] }, error: undefined }));
		await waitFor(() => expect(result.current.requirementsBlocked).toBe(true));

		// The supervisor invalidates this cache on a daemon identity change,
		// including a restart that binds the same port again.
		setBaseUrl("");
		act(() => queryClient.removeQueries({ queryKey: systemRequirementsQueryKey }));
		getMock.mockResolvedValueOnce({ data: { requirements: [] }, error: undefined } as never);
		setBaseUrl("http://127.0.0.1:4555");
		await waitFor(() => expect(getMock).toHaveBeenCalledTimes(3));
		await waitFor(() => expect(result.current.ready).toBe(true));
	});
});

