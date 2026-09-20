import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock, patchMock } = vi.hoisted(() => ({ getMock: vi.fn(), patchMock: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, PATCH: patchMock },
	apiErrorMessage: (_error: unknown, fallback = "Request failed") => fallback,
}));

import { settingsQueryKey, useSettings, useUpdateAutoPause, useUpdateMemoryBudget } from "./useSettings";

function wrapper(client: QueryClient) {
	return ({ children }: { children: ReactNode }) => (
		<QueryClientProvider client={client}>{children}</QueryClientProvider>
	);
}

beforeEach(() => {
	getMock.mockReset().mockResolvedValue({ data: { defaultSessionMode: "tui", chatHarnesses: [], autoPauseIdleMinutes: 30 } });
	patchMock.mockReset().mockResolvedValue({ data: {} });
});

describe("auto-pause setting", () => {
	it("reads the daemon's threshold and defaults to off on an older daemon", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const { result } = renderHook(() => useSettings(), { wrapper: wrapper(client) });
		await waitFor(() => expect(result.current.settings?.autoPauseIdleMinutes).toBe(30));

		getMock.mockResolvedValue({ data: { defaultSessionMode: "tui", chatHarnesses: [] } });
		await act(async () => {
			await client.invalidateQueries({ queryKey: settingsQueryKey });
		});
		await waitFor(() => expect(result.current.settings?.autoPauseIdleMinutes).toBe(0));
	});

	it("patches the memory budget in bytes and refetches", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const invalidate = vi.spyOn(client, "invalidateQueries");
		const { result } = renderHook(() => useUpdateMemoryBudget(), { wrapper: wrapper(client) });
		act(() => result.current.update(8 * 1024 ** 3));
		await waitFor(() =>
			expect(patchMock).toHaveBeenCalledWith("/api/v1/settings/memory-budget", { body: { bytes: 8 * 1024 ** 3 } }),
		);
		await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: settingsQueryKey }));
	});

	it("patches the threshold and refetches rather than trusting the local value", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const invalidate = vi.spyOn(client, "invalidateQueries");
		const { result } = renderHook(() => useUpdateAutoPause(), { wrapper: wrapper(client) });
		act(() => result.current.update(15));
		await waitFor(() =>
			expect(patchMock).toHaveBeenCalledWith("/api/v1/settings/auto-pause", { body: { idleMinutes: 15 } }),
		);
		await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: settingsQueryKey }));
	});
});
