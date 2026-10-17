import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock, patchMock } = vi.hoisted(() => ({ getMock: vi.fn(), patchMock: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, PATCH: patchMock },
	apiErrorMessage: () => "request failed",
}));

import { settingsQueryKey, useSettings, useUpdateCardSummaries, type Settings } from "./useSettings";

function wrapper(queryClient: QueryClient) {
	return ({ children }: { children: ReactNode }) => (
		<QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
	);
}

beforeEach(() => {
	getMock.mockReset();
	patchMock.mockReset();
});

describe("card summaries toggle", () => {
	it.each([false, true])("responds before saving when initially %s", async (initial) => {
		let finish!: (value: { data: { cardSummaries: boolean } }) => void;
		patchMock.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
		queryClient.setQueryData(settingsQueryKey, { cardSummaries: initial, cloudOffering: true });
		queryClient.setQueryData(["settings", "remote-host"], { cardSummaries: initial });
		queryClient.setQueryData(["workspaces"], []);
		// A slow reconciliation read must not delay the switch either.
		getMock.mockReturnValue(new Promise(() => {}));
		const { result } = renderHook(() => ({ ...useSettings(), ...useUpdateCardSummaries() }), { wrapper: wrapper(queryClient) });

		act(() => result.current.update(!initial));
		await waitFor(() => expect(result.current.settings?.cardSummaries).toBe(!initial));
		expect(result.current.saving).toBe(true);
		expect(patchMock).toHaveBeenCalledWith("/api/v1/settings/card-summaries", { body: { enabled: !initial } });
		expect(queryClient.getQueryData(["settings", "remote-host"])).toEqual({ cardSummaries: initial });

		await act(async () => finish({ data: { cardSummaries: !initial } }));
		await waitFor(() => expect(result.current.saving).toBe(false));
		expect(result.current.settings?.cardSummaries).toBe(!initial);
		expect(queryClient.getQueryState(["workspaces"])?.isInvalidated).toBe(true);
		queryClient.clear();
	});

	it("rolls back a failed save without losing other settings changes", async () => {
		let finish!: (value: { error: object }) => void;
		patchMock.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
		getMock.mockReturnValue(new Promise(() => {}));
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
		queryClient.setQueryData(settingsQueryKey, { cardSummaries: false, cloudOffering: false });
		const { result } = renderHook(() => ({ ...useSettings(), ...useUpdateCardSummaries() }), { wrapper: wrapper(queryClient) });

		act(() => result.current.update(true));
		await waitFor(() => expect(result.current.settings?.cardSummaries).toBe(true));
		act(() => queryClient.setQueryData<Settings>(settingsQueryKey, (current) => current ? { ...current, cloudOffering: true } : current));
		await act(async () => finish({ error: { message: "save failed" } }));
		await waitFor(() => expect(result.current.error).toBe("request failed"));
		expect(result.current.settings?.cardSummaries).toBe(false);
		expect(result.current.settings?.cloudOffering).toBe(true);
		queryClient.clear();
	});
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
		expect(getMock).toHaveBeenCalledWith("/api/v1/settings");
	});
});
