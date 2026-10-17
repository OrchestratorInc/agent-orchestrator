import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";
import { clientForHost } from "../lib/host-clients";
import { LOCAL_HOST } from "../lib/hosts";

export type ProjectReport = components["schemas"]["ReportResponse"];

export type ReportDeliverySummary = { undelivered: number; errored: number };

export const projectReportsQueryRoot = ["project-reports"] as const;
export const projectReportsQueryKey = (projectId: string, hostId?: string) =>
	[...projectReportsQueryRoot, hostId ?? LOCAL_HOST, projectId] as const;

export async function fetchProjectReports(projectId: string, hostId?: string): Promise<ProjectReport[]> {
	const { data, error } = await (hostId ? clientForHost(hostId) : apiClient).GET("/api/v1/reports", {
		params: { query: { projectId } },
	});
	if (error) throw error;
	return data?.reports ?? [];
}

// Delivery smoke counts for the developer-mode board indicator: everything the
// outbox has not acknowledged yet, and how many of those already failed once.
export function summarizeReportDelivery(reports: ProjectReport[]): ReportDeliverySummary {
	let undelivered = 0;
	let errored = 0;
	for (const report of reports) {
		if (report.deliveryState === "acknowledged") continue;
		undelivered += 1;
		if (report.lastError) errored += 1;
	}
	return { undelivered, errored };
}

export function useProjectReports(projectId: string, hostId: string | undefined, enabled: boolean) {
	return useQuery({
		queryKey: projectReportsQueryKey(projectId, hostId),
		queryFn: () => fetchProjectReports(projectId, hostId),
		enabled,
		retry: 1,
		refetchInterval: 15_000,
		select: summarizeReportDelivery,
	});
}
