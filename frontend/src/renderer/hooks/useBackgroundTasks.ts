import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";

export type BackgroundTaskReading = components["schemas"]["BackgroundTasksResponse"];
export type BackgroundTask = components["schemas"]["BackgroundTaskResponse"];
export const backgroundTasksQueryRoot = ["background-tasks"] as const;

function isTaskReading(data: BackgroundTaskReading | undefined): data is BackgroundTaskReading {
	return !!data && typeof data.sessionId === "string" && Array.isArray(data.tasks)
		&& ["ready", "partial", "unavailable"].includes(data.availability);
}

const selectReadings = (data: components["schemas"]["ListBackgroundTasksResponse"]) =>
	new Map(data.sessions.map((reading) => [reading.sessionId, reading]));

/** One batch query for the local board, independent of Diagnostics. */
export function useBackgroundTaskReadings(enabled: boolean, projectId?: string) {
	return useQuery({
		queryKey: [...backgroundTasksQueryRoot, "all", projectId ?? null],
		enabled,
		queryFn: async ({ signal }) => {
			const { data, error } = await apiClient.GET("/api/v1/sessions/background-tasks", {
				params: { query: projectId ? { projectId } : {} }, signal,
			});
			if (error) throw error;
			if (!data || !Array.isArray(data.sessions) || !data.sessions.every(isTaskReading)) {
				throw new Error("Invalid background task inventory response");
			}
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
			if (!isTaskReading(data)) throw new Error("Invalid background task response");
			return data;
		},
		refetchInterval: fast ? 2_000 : 5_000,
		staleTime: 1_000,
		retry: false,
	});
}

/**
 * Stops one provider-owned task. A 409 means it already settled or this
 * provider cannot stop it: the next reading tells the truth, so it is not an error.
 */
export function useStopBackgroundTask(sessionId: string) {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: async (taskId: string) => {
			const { error, response } = await apiClient.POST("/api/v1/sessions/{sessionId}/background-tasks/{taskId}/stop", {
				params: { path: { sessionId, taskId } },
			});
			if (error && response.status !== 409) throw error;
		},
		onSettled: () => queryClient.invalidateQueries({ queryKey: backgroundTasksQueryRoot }),
	});
}
