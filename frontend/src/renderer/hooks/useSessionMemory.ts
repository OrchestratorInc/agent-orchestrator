import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";

export type SessionMemoryReading = components["schemas"]["SessionMemoryResponse"];
export type SystemMemoryReading = components["schemas"]["SystemMemoryResponse"];
export type AppMemoryReading = components["schemas"]["AppMemoryResponse"];
export type MemoryReserveReading = components["schemas"]["MemoryReserveResponse"];

export const sessionMemoryQueryRoot = ["session-memory"] as const;
export const sessionMemoryQueryKey = (projectId?: string) =>
	[...sessionMemoryQueryRoot, projectId ?? "all"] as const;

/** Memory is a live reading, so the board keeps it fresh like a process monitor. */
export const sessionMemoryRefetchIntervalMs = 5_000;

type SessionMemoryResponse = {
	sessions: SessionMemoryReading[];
	system?: SystemMemoryReading;
	app?: AppMemoryReading;
	reserve?: MemoryReserveReading;
};

export async function fetchSessionMemory(projectId?: string): Promise<SessionMemoryResponse> {
	const { data, error } = await apiClient.GET("/api/v1/usage/sessions/memory", {
		params: { query: projectId ? { projectId } : {} },
	});
	if (error) throw error;
	return { sessions: data?.sessions ?? [], system: data?.system, app: data?.app, reserve: data?.reserve };
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

/** Everything AO runs, app-wide, for the status bar. Same query as the
 * sessions so the bar and the panel it opens never disagree. */
export function useAppMemory() {
	return useQuery({
		...sessionMemoryQueryOptions(),
		select: (data: SessionMemoryResponse) => ({
			app: data.app,
			system: data.system,
			reserve: data.reserve,
			// Sessions with a live runtime: a paused one has none, so it is not counted.
			liveCount: data.sessions.length,
		}),
	});
}

/** Why the light is the colour it is: the worst of memory, swap and CPU. */
export type PressureReason = "memory" | "swap" | "cpu";
export type MemoryPressure = {
	tone: MemoryTone;
	reason: PressureReason;
	/** Share of host RAM the kernel could still hand out. */
	freePct: number;
	/** One-minute load per core; above 1 work is queueing. */
	cpuLoad: number;
	/** Whether pages are actively moving to or from swap. */
	swapping: boolean;
	/** Free RAM sits under the user's reserve: auto-started spawns are on hold. */
	belowReserve: boolean;
};

/** Actively swapping means more than this much moving per second: a few stray
 * pages are normal, a megabyte a second is the frozen-cursor signal. */
const SWAPPING_BYTES_PER_SEC = 1024 ** 2;

/**
 * The light reads the whole machine's headroom, never AO's share: the OS's
 * "available" already accounts for everyone else, and a host about to swap
 * is red whoever holds the memory. Over a quarter free is green; under a
 * tenth, or actively swapping, is red. CPU pinned for a while only ever
 * makes it yellow: slow is not the same as frozen.
 */
export function memoryPressure(system: SystemMemoryReading, reserveBytes?: number): MemoryPressure {
	const { totalBytes, availableBytes, swapBytesPerSec, cpuCount, load1 } = system;
	const freePct = totalBytes > 0 ? Math.round((availableBytes / totalBytes) * 100) : 100;
	const cpuLoad = cpuCount > 0 ? load1 / cpuCount : 0;
	const swapping = swapBytesPerSec >= SWAPPING_BYTES_PER_SEC;
	const belowReserve = reserveBytes !== undefined && reserveBytes > 0 && availableBytes < reserveBytes;
	let tone: MemoryTone = "default";
	let reason: PressureReason = "memory";
	if (swapping) {
		tone = "critical";
		reason = "swap";
	} else if (freePct < 10) {
		tone = "critical";
	} else if (freePct < 25) {
		tone = "warning";
	} else if (cpuLoad >= 1) {
		tone = "warning";
		reason = "cpu";
	}
	return { tone, reason, freePct, cpuLoad, swapping, belowReserve };
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

/** Whole percent of one core; a tenth below one percent so "0%" never lies. */
export function formatCPU(percent: number): string {
	if (percent >= 1 || percent === 0) return `${Math.round(percent)}%`;
	return `${percent.toFixed(1)}%`;
}
