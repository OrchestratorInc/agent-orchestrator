import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";

export type BackgroundTaskReading = components["schemas"]["BackgroundTasksResponse"];
export type BackgroundTask = components["schemas"]["BackgroundTaskResponse"];
export const backgroundTasksQueryRoot = ["background-tasks"] as const;

const selectReadings = (data: components["schemas"]["ListBackgroundTasksResponse"]) =>
	new Map(data.sessions.map((reading) => [reading.sessionId, reading]));

/** One batch query for the local board, independent of Diagnostics. */
export function useBackgroundTaskReadings(enabled: boolean) {
	return useQuery({
		queryKey: [...backgroundTasksQueryRoot, "all"],
		enabled,
		queryFn: async ({ signal }) => {
			const { data, error } = await apiClient.GET("/api/v1/sessions/background-tasks", { signal });
			if (error) throw error;
			return data;
		},
		select: selectReadings,
		refetchInterval: 5_000,
		staleTime: 1_000,
		retry: false,
	});
}

/** Only attached providers are inspected; this read never wakes an agent. */
export function useBackgroundTasks(sessionId: string, enabled: boolean, fast = false) {
	return useQuery({
		queryKey: [...backgroundTasksQueryRoot, sessionId],
		enabled,
		queryFn: async ({ signal }) => {
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/background-tasks", {
				params: { path: { sessionId } }, signal,
			});
			if (error) throw error;
			return data;
		},
		refetchInterval: fast ? 2_000 : 5_000,
		staleTime: 1_000,
		retry: false,
	});
}
