import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";

export type SessionMemoryReading = components["schemas"]["SessionMemoryResponse"];
export type SystemMemoryReading = components["schemas"]["SystemMemoryResponse"];
export type AppMemoryReading = components["schemas"]["AppMemoryResponse"];

export const sessionMemoryQueryRoot = ["session-memory"] as const;
export const sessionMemoryQueryKey = (projectId?: string) =>
	[...sessionMemoryQueryRoot, projectId ?? "all"] as const;

/** Memory is a live reading, so the board keeps it fresh like a process monitor. */
export const sessionMemoryRefetchIntervalMs = 5_000;

type SessionMemoryResponse = {
	sessions: SessionMemoryReading[];
	system?: SystemMemoryReading;
	app?: AppMemoryReading;
};

export async function fetchSessionMemory(projectId?: string): Promise<SessionMemoryResponse> {
	const { data, error } = await apiClient.GET("/api/v1/usage/sessions/memory", {
		params: { query: projectId ? { projectId } : {} },
	});
	if (error) throw error;
	return { sessions: data?.sessions ?? [], system: data?.system, app: data?.app };
}

export function sessionMemoryQueryOptions(projectId?: string) {
	return {
		queryKey: sessionMemoryQueryKey(projectId),
		queryFn: () => fetchSessionMemory(projectId),
		refetchInterval: sessionMemoryRefetchIntervalMs,
		// 501 on Windows is permanent for the run; do not hammer the daemon.
		retry: false,
	};
}

export function useSessionMemory(projectId?: string) {
	return useQuery({
		...sessionMemoryQueryOptions(projectId),
		select: (data: SessionMemoryResponse) =>
			new Map(data.sessions.map((item) => [item.sessionId, item] as const)),
	});
}

/** Host RAM for the panel's total bar. Shares the session-memory query, so
 * mounting both hooks costs one fetch, not two. Absent where unsupported. */
export function useSystemMemory(projectId?: string) {
	return useQuery({
		...sessionMemoryQueryOptions(projectId),
		select: (data: SessionMemoryResponse) => data.system,
	});
}

/** Everything AO runs, app-wide, for the topbar indicator. Same query as the
 * sessions so the indicator and the panel it opens never disagree. */
export function useAppMemory() {
	return useQuery({
		...sessionMemoryQueryOptions(),
		select: (data: SessionMemoryResponse) => ({ app: data.app, system: data.system }),
	});
}

export type MemoryPressure = { pct: number; freePct: number; tone: MemoryTone };

/** AO's share of host RAM, colored by the worse of two signals: how much of
 * the machine AO holds (a tenth is worth a glance, a quarter is a problem),
 * and how little the host has left regardless of who holds it. */
export function memoryPressure(usedBytes: number, system: { totalBytes: number; availableBytes: number }): MemoryPressure {
	const { totalBytes, availableBytes } = system;
	const pct = totalBytes > 0 ? Math.min(100, Math.round((usedBytes / totalBytes) * 100)) : 0;
	const freePct = totalBytes > 0 ? Math.round((availableBytes / totalBytes) * 100) : 100;
	const shareTone: MemoryTone = pct >= 25 ? "critical" : pct >= 10 ? "warning" : "default";
	const hostTone: MemoryTone = freePct < 7 ? "critical" : freePct < 15 ? "warning" : "default";
	return { pct, freePct, tone: worseTone(shareTone, hostTone) };
}

const toneRank: Record<MemoryTone, number> = { default: 0, warning: 1, critical: 2 };
function worseTone(a: MemoryTone, b: MemoryTone): MemoryTone {
	return toneRank[a] >= toneRank[b] ? a : b;
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
