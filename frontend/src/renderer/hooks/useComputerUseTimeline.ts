import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, getApiBaseUrl } from "../lib/api-client";

export type ComputerUseTimelineEntry = components["schemas"]["ComputerUseTimelineEntry"];

export const computerUseTimelineQueryKey = (sessionId: string) => ["computer-use-timeline", sessionId] as const;

/**
 * A local session's computer-use calls, oldest first, from the daemon's log of
 * them. Read once for every local session so past steps show even with the
 * setting off; polled while agents may use the computer, faster while the
 * Computer Use tab is open.
 */
export function useComputerUseTimeline(sessionId: string | undefined, { live, open }: { live: boolean; open: boolean }) {
	return useQuery({
		queryKey: computerUseTimelineQueryKey(sessionId ?? ""),
		enabled: sessionId !== undefined,
		refetchInterval: live ? (open ? 2_000 : 8_000) : false,
		// A daemon without the route (or a refused read) means no timeline, not an error to show.
		retry: false,
		queryFn: async ({ signal }) => {
			const { data, error } = await apiClient.GET("/api/v1/computer-use/sessions/{sessionId}/timeline", {
				params: { path: { sessionId: sessionId! } },
				signal,
			});
			if (error) throw error;
			return data?.entries ?? [];
		},
	});
}

/** The daemon URL of one of a session's saved screenshots. */
export function computerUseScreenshotUrl(sessionId: string, name: string): string {
	return `${getApiBaseUrl()}/api/v1/computer-use/sessions/${encodeURIComponent(sessionId)}/screenshots/${encodeURIComponent(name)}`;
}
