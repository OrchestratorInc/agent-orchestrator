import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";

export type SessionMemoryReading = components["schemas"]["SessionMemoryResponse"];

export const sessionMemoryQueryRoot = ["session-memory"] as const;
export const sessionMemoryQueryKey = (projectId?: string) =>
	[...sessionMemoryQueryRoot, projectId ?? "all"] as const;

/** Memory is a live reading, so the board keeps it fresh like a process monitor. */
export const sessionMemoryRefetchIntervalMs = 5_000;

export async function fetchSessionMemory(projectId?: string): Promise<SessionMemoryReading[]> {
	const { data, error } = await apiClient.GET("/api/v1/usage/sessions/memory", {
		params: { query: projectId ? { projectId } : {} },
	});
	if (error) throw error;
	return data?.sessions ?? [];
}

export function sessionMemoryQueryOptions(projectId?: string) {
	return {
		queryKey: sessionMemoryQueryKey(projectId),
		queryFn: () => fetchSessionMemory(projectId),
		refetchInterval: sessionMemoryRefetchIntervalMs,
		// 501 on Windows is permanent for the run; do not hammer the daemon.
		retry: false,
		select: (items: SessionMemoryReading[]) =>
			new Map(items.map((item) => [item.sessionId, item] as const)),
	};
}

export function useSessionMemory(projectId?: string) {
	return useQuery(sessionMemoryQueryOptions(projectId));
}

const GIB = 1024 ** 3;
const MIB = 1024 ** 2;

/** Above one gigabyte a session is worth a glance; above two it is the one to kill. */
export type MemoryTone = "default" | "warning" | "critical";

export function memoryTone(rssBytes: number): MemoryTone {
	if (rssBytes >= 2 * GIB) return "critical";
	if (rssBytes >= GIB) return "warning";
	return "default";
}

/** Binary units, one decimal above a gigabyte, whole megabytes below — the btop convention. */
export function formatMemory(rssBytes: number): string {
	if (rssBytes >= GIB) return `${(rssBytes / GIB).toFixed(1)} GB`;
	if (rssBytes >= MIB) return `${Math.round(rssBytes / MIB)} MB`;
	return `${Math.max(1, Math.round(rssBytes / 1024))} KB`;
}
