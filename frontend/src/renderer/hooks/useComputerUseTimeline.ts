import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, getApiBaseUrl } from "../lib/api-client";
import { isLocal } from "../lib/hosts";
import type { WorkspaceSession } from "../types/workspace";

export type ComputerUseTimelineEntry = components["schemas"]["ComputerUseTimelineEntry"];

export const computerUseTimelineQueryKey = (sessionId: string) => ["computer-use-timeline", sessionId] as const;

/**
 * The id of a session whose computer-use timeline this daemon keeps, else
 * undefined. Computer use runs on this machine, so only a local, non-cloud
 * session has one, whether it is named with no host or with the local host's
 * id. The inspector tab and the view that opens it share this check.
 */
export function localComputerUseSessionId(session: WorkspaceSession | undefined, hostId: string | undefined): string | undefined {
	return session && !session.cloud && (!hostId || isLocal(hostId)) ? session.id : undefined;
}

/**
 * A local session's computer-use calls, oldest first, from the daemon's log of
 * them. Read once for every local session so past steps show even with the
 * setting off; polled while the session runs and agents may use the computer
 * (`live`), faster while the Computer Use tab is open.
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
