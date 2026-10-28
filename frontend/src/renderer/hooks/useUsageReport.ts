import { keepPreviousData, useQuery } from "@tanstack/react-query";
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
