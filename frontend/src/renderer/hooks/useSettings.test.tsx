import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock },
	apiErrorMessage: () => "request failed",
}));

import { settingsQueryKey, useSettings, useSettingsPolling } from "./useSettings";

function wrapper(queryClient: QueryClient) {
	return ({ children }: { children: ReactNode }) => (
		<QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
	);
}

beforeEach(() => {
	getMock.mockReset();
});

describe("useSettings tracker intake gate", () => {
	it.each([
		[true, true],
		[false, false],
		[undefined, false],
	])("maps trackerIntakeEnabled=%s to %s", async (trackerIntakeEnabled, expected) => {
		getMock.mockResolvedValue({
			data: { trackerIntakeEnabled },
			error: undefined,
		});
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const { result } = renderHook(() => useSettings(), { wrapper: wrapper(queryClient) });

		await waitFor(() => expect(result.current.settings?.trackerIntakeEnabled).toBe(expected));
		expect(getMock).toHaveBeenCalledWith("/api/v1/settings", { signal: expect.any(AbortSignal) });
	});
});

describe("useSettings polling", () => {
	afterEach(() => {
		vi.useRealTimers();
	});

	it("keeps one poll and one stale timer for the local settings however many components read them", async () => {
		vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval"] });
		getMock.mockResolvedValue({ data: {}, error: undefined });
		// The app's default stale time, which gives every fresh observer a stale timer.
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 10_000 } } });
		const { result } = renderHook(() => {
			useSettingsPolling();
			return [...Array.from({ length: 10 }, () => useSettings()), ...Array.from({ length: 10 }, () => useSettings(undefined, false))];
		}, { wrapper: wrapper(queryClient) });

		await act(async () => {
			await vi.advanceTimersByTimeAsync(0);
		});
		expect(result.current[0]?.settings).toBeDefined();
		const observers = queryClient.getQueryCache().find({ queryKey: settingsQueryKey, exact: true })?.observers ?? [];
		expect(observers).toHaveLength(21);
		expect(observers.filter((observer) => observer.options.refetchInterval)).toHaveLength(1);
		// The poll, its stale timer and TanStack's own; 5 or 40 readers give the same
		// count, enabled or not. Each reader used to add an interval and a stale timer.
		expect(vi.getTimerCount()).toBeLessThanOrEqual(3);
		expect(getMock).toHaveBeenCalledTimes(1);

		await act(async () => {
			await vi.advanceTimersByTimeAsync(15_000);
		});
		expect(getMock).toHaveBeenCalledTimes(2);
	});

	it("refetches on mount only once the settings are older than the client's stale time", async () => {
		vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval", "Date"] });
		getMock.mockResolvedValue({ data: {}, error: undefined });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 10_000 } } });
		queryClient.setQueryData(settingsQueryKey, { defaultSessionMode: "tui" });

		const first = renderHook(() => useSettings(), { wrapper: wrapper(queryClient) });
		await act(async () => {
			await vi.advanceTimersByTimeAsync(0);
		});
		expect(getMock).not.toHaveBeenCalled();
		expect(vi.getTimerCount()).toBe(0);

		await act(async () => {
			await vi.advanceTimersByTimeAsync(10_001);
		});
		renderHook(() => useSettings(), { wrapper: wrapper(queryClient) });
		await act(async () => {
			await vi.advanceTimersByTimeAsync(0);
		});
		expect(getMock).toHaveBeenCalledTimes(1);
		first.unmount();
	});
});
