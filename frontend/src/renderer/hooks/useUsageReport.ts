import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import type { UsageReport } from "../lib/usage-report";

export const usageReportQueryKey = ["usage", "report"] as const;

/** The browser's IANA zone, so days and hours match the user's clock. */
export function localTimeZone(): string {
	return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
}

/** Usage over the last `days` days (1 reads the last 24 hours by hour), optionally for one project. */
export function useUsageReport(days: number, projectId: string | undefined) {
	const timeZone = localTimeZone();
	return useQuery({
		queryKey: [...usageReportQueryKey, days, timeZone, projectId ?? ""],
		queryFn: async (): Promise<UsageReport> => {
			const { data, error } = await apiClient.GET("/api/v1/usage/report", {
				params: { query: { days, tz: timeZone, ...(projectId ? { projectId } : {}) } },
			});
			if (error) throw new Error(apiErrorMessage(error));
			return data as UsageReport;
		},
		placeholderData: keepPreviousData,
		refetchInterval: 60_000,
	});
}
