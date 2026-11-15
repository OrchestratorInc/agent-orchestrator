import { keepPreviousData, useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";
import { apiErrorException } from "../lib/api-error-exception";
import type { UsageReport } from "../lib/usage-report";
import type { UsageLimitWindows } from "../lib/usage-limits";

export const usageReportQueryKey = ["usage", "report"] as const;

export function localTimeZone(): string {
	return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
}

export function useUsageReport(days: number, offset: number, projectId: string | undefined) {
	const timeZone = localTimeZone();
	return useQuery({
		queryKey: [...usageReportQueryKey, days, offset, timeZone, projectId ?? ""],
		queryFn: async (): Promise<UsageReport> => {
			const { data, error } = await apiClient.GET("/api/v1/usage/report", {
				params: { query: { days, offset, tz: timeZone, ...(projectId ? { projectId } : {}) } },
			});
			if (error) throw apiErrorException(error);
			return data as UsageReport;
		},
		placeholderData: keepPreviousData,
		refetchInterval: 60_000,
	});
}

export function useUsageLimitWindows(days: number, enabled: boolean) {
	return useQuery({
		queryKey: [...usageReportQueryKey, "limit-windows", days],
		queryFn: async (): Promise<UsageLimitWindows> => {
			const { data, error } = await apiClient.GET("/api/v1/usage/limit-windows", { params: { query: { days } } });
			if (error) throw apiErrorException(error);
			return data as UsageLimitWindows;
		},
		enabled,
		refetchInterval: 60_000,
	});
}

export type UsagePriceOverride = components["schemas"]["UsagePriceOverride"];
export type UsagePrices = components["schemas"]["UsagePricesResponse"];
const usagePricesQueryKey = ["usage", "prices"] as const;

export function useUsagePrices() {
	return useQuery({
		queryKey: usagePricesQueryKey,
		queryFn: async (): Promise<UsagePrices> => {
			const { data, error } = await apiClient.GET("/api/v1/usage/prices");
			if (error) throw apiErrorException(error);
			return data;
		},
	});
}

export function useSaveUsagePrices() {
	const queryClient = useQueryClient();
	return async (overrides: UsagePriceOverride[]) => {
		const { data, error } = await apiClient.PUT("/api/v1/usage/prices", { body: { overrides } });
		if (error) throw apiErrorException(error);
		queryClient.setQueryData(usagePricesQueryKey, data);
		await queryClient.invalidateQueries({ queryKey: usageReportQueryKey });
		window.setTimeout(() => void queryClient.invalidateQueries({ queryKey: usageReportQueryKey }), 2_000);
	};
}
