import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";

export type BackgroundTaskReading = components["schemas"]["BackgroundTasksResponse"];
export type BackgroundTask = components["schemas"]["BackgroundTaskResponse"];
export const backgroundTasksQueryRoot = ["background-tasks"] as const;

function isTaskReading(data: BackgroundTaskReading | undefined): data is BackgroundTaskReading {
	return !!data && typeof data.sessionId === "string" && Array.isArray(data.tasks)
		&& data.tasks.every((task) => !!task && typeof task === "object")
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
			// observedAt changes on every sample and the board does not use it.
			// Dropping it lets structural sharing keep unchanged readings, so the
			// board does not re-render on every poll.
			return { ...data, sessions: data.sessions.map(({ observedAt: _observedAt, ...reading }) => reading) };
		},
		select: selectReadings,
		refetchInterval: 5_000,
		staleTime: 1_000,
		retry: false,
	});
}

/**
 * Only attached providers are inspected; this read never wakes an agent.
 * `fast` reads every 2 s, for the side panel's Background tasks tab.
 */
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
		onSettled: () => queryClient.invalidateQueries({ queryKey: [...backgroundTasksQueryRoot, sessionId] }),
	});
}

/** `lastRead`: the daemon no longer has this output, so it is the last one read. */
export type BackgroundTaskOutput = components["schemas"]["BackgroundTaskOutputResponse"] & { lastRead?: boolean };

/** One run's output: a rerun under the same task id never shows the previous run's. */
export const backgroundTaskOutputQueryKey = (sessionId: string, taskId: string, runId?: string) =>
	["background-task-output", sessionId, taskId, runId ?? ""] as const;

/**
 * One task's latest output (the daemon keeps the last 200 lines). Polls every
 * second while `live`; otherwise reads once. Unchanged output keeps the same
 * object, so an idle task does not re-render its tab.
 *
 * Once the task is `over`, output the daemon no longer has (the chat ended or
 * the daemon restarted) stays as last read. The query is never collected, so
 * reopening the task much later still shows it. While the task runs, such a
 * read is a failed poll instead: the output read so far stays, with the error.
 */
export function useBackgroundTaskOutput(
	sessionId: string,
	taskId: string,
	runId: string | undefined,
	{ enabled, live, over }: { enabled: boolean; live: boolean; over: boolean },
) {
	const queryClient = useQueryClient();
	const queryKey = backgroundTaskOutputQueryKey(sessionId, taskId, runId);
	return useQuery({
		queryKey,
		enabled,
		queryFn: async ({ signal }) => {
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/background-tasks/{taskId}/output", {
				params: { path: { sessionId, taskId } }, signal,
			});
			if (error) throw error;
			if (!data || typeof data.output !== "string" || (data.availability !== "ready" && data.availability !== "unavailable")) {
				throw new Error("Invalid background task output response");
			}
			const previous = queryClient.getQueryData<BackgroundTaskOutput>(queryKey);
			if (data.availability === "unavailable" && previous?.availability === "ready" && previous.output) {
				if (!over) throw new Error("Background task output unavailable");
				return previous.lastRead ? previous : { ...previous, lastRead: true };
			}
			return data as BackgroundTaskOutput;
		},
		refetchInterval: live ? 1_000 : false,
		gcTime: Infinity,
		retry: false,
	});
}
