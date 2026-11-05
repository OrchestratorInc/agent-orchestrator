import { keepPreviousData, useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import type { UsageReport } from "../lib/usage-report";
import type { UsageLimitWindows } from "../lib/usage-limits";

export const usageReportQueryKey = ["usage", "report"] as const;

/** The browser's IANA zone, so days and hours match the user's clock. */
export function localTimeZone(): string {
	return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
}

/**
 * Usage over the last `days` days (1 reads the last 24 hours by hour), stepped
 * back `offset` whole windows, optionally for one project.
 */
export function useUsageReport(days: number, offset: number, projectId: string | undefined) {
	const timeZone = localTimeZone();
	return useQuery({
		queryKey: [...usageReportQueryKey, days, offset, timeZone, projectId ?? ""],
		queryFn: async (): Promise<UsageReport> => {
			const { data, error } = await apiClient.GET("/api/v1/usage/report", {
				params: { query: { days, offset, tz: timeZone, ...(projectId ? { projectId } : {}) } },
			});
			if (error) throw new Error(apiErrorMessage(error));
			return data as UsageReport;
		},
		placeholderData: keepPreviousData,
		refetchInterval: 60_000,
	});
}

/** Each login's limit windows over the last `days` days, from AO's own limit readings. */
export function useUsageLimitWindows(days: number, enabled: boolean) {
	return useQuery({
		queryKey: [...usageReportQueryKey, "limit-windows", days],
		queryFn: async (): Promise<UsageLimitWindows> => {
			const { data, error } = await apiClient.GET("/api/v1/usage/limit-windows", { params: { query: { days } } });
			if (error) throw new Error(apiErrorMessage(error));
			return data as UsageLimitWindows;
		},
		enabled,
		refetchInterval: 60_000,
	});
}

export type UsagePriceOverride = components["schemas"]["UsagePriceOverride"];
export type UsagePrices = components["schemas"]["UsagePricesResponse"];
const usagePricesQueryKey = ["usage", "prices"] as const;

/** The user's model prices, and the catalog models a model can price as. */
export function useUsagePrices() {
	return useQuery({
		queryKey: usagePricesQueryKey,
		queryFn: async (): Promise<UsagePrices> => {
			const { data, error } = await apiClient.GET("/api/v1/usage/prices");
			if (error) throw new Error(apiErrorMessage(error));
			return data;
		},
	});
}

/** Saves the whole set of model prices; the daemon reprices their usage. */
export function useSaveUsagePrices() {
	const queryClient = useQueryClient();
	return async (overrides: UsagePriceOverride[]) => {
		const { data, error } = await apiClient.PUT("/api/v1/usage/prices", { body: { overrides } });
		if (error) throw new Error(apiErrorMessage(error));
		queryClient.setQueryData(usagePricesQueryKey, data);
		// Repricing runs in the background; look again once it has had a moment.
		await queryClient.invalidateQueries({ queryKey: usageReportQueryKey });
		window.setTimeout(() => void queryClient.invalidateQueries({ queryKey: usageReportQueryKey }), 2_000);
	};
}

export type CursorUsageStatus = components["schemas"]["CursorUsageStatus"];
const cursorUsageQueryKey = ["usage", "cursor"] as const;

/** Whether AO reads Cursor usage from cursor.com, and how the last read went. */
export function useCursorUsage() {
	return useQuery({
		queryKey: cursorUsageQueryKey,
		queryFn: async (): Promise<CursorUsageStatus> => {
			const { data, error } = await apiClient.GET("/api/v1/usage/cursor");
			if (error) throw new Error(apiErrorMessage(error));
			return data;
		},
		refetchInterval: 60_000,
	});
}

/** Turns reading Cursor usage on, which reads at once, or off. */
export function useSetCursorUsage() {
	const queryClient = useQueryClient();
	return async (enabled: boolean) => {
		const { data, error } = await apiClient.PUT("/api/v1/usage/cursor", { body: { enabled } });
		if (error) throw new Error(apiErrorMessage(error));
		queryClient.setQueryData(cursorUsageQueryKey, data);
		// The first read runs in the background; look again once it has had a moment.
		window.setTimeout(() => {
			void queryClient.invalidateQueries({ queryKey: cursorUsageQueryKey });
			void queryClient.invalidateQueries({ queryKey: usageReportQueryKey });
		}, 5_000);
	};
}
