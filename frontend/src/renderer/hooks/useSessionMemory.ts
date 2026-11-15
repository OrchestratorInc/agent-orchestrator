import { useEffect, useSyncExternalStore } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { formatResourceBytes, pressureStateFromRaw, type PressureState } from "@aoagents/product-ui";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";
import { useUiStore } from "../stores/ui-store";

export type SessionMemoryReading = components["schemas"]["SessionMemoryResponse"];
export type SessionStepReading = components["schemas"]["SessionStepResponse"];
export type SystemMemoryReading = components["schemas"]["SystemMemoryResponse"];
export type AppMemoryReading = components["schemas"]["AppMemoryResponse"];
export type ReviewerMemoryReading = components["schemas"]["ControllersReviewerMemoryResponse"];
export type MemoryPressureReading = components["schemas"]["MemoryPressureResponse"];

export const sessionMemoryQueryRoot = ["session-memory"] as const;
export const sessionMemoryQueryKey = (projectId?: string) =>
	[...sessionMemoryQueryRoot, projectId ?? "all"] as const;

export const sessionMemoryRefetchIntervalMs = 60_000;
export const sessionMemoryFastRefetchIntervalMs = 2_000;

type SessionMemoryResponse = {
	sessions: SessionMemoryReading[];
	system?: SystemMemoryReading;
	app?: AppMemoryReading;
	fetchedAt: string;
};

export async function fetchSessionMemory(projectId?: string): Promise<SessionMemoryResponse> {
	const { data, error } = await apiClient.GET("/api/v1/usage/sessions/memory", {
		params: { query: projectId ? { projectId } : {} },
	});
	if (error) throw error;
	const reading = { sessions: data?.sessions ?? [], system: data?.system, app: data?.app, fetchedAt: new Date().toISOString() };
	if (!projectId) recordCPU(reading);
	return reading;
}

const fastWatchers = { count: 0 };

export function sessionMemoryQueryOptions(projectId?: string) {
	return {
		queryKey: sessionMemoryQueryKey(projectId),
		queryFn: () => fetchSessionMemory(projectId),
		refetchInterval: () => (fastWatchers.count > 0 ? sessionMemoryFastRefetchIntervalMs : sessionMemoryRefetchIntervalMs),
		retry: false,
	};
}

export function useFastMemorySampling() {
	const queryClient = useQueryClient();
	useEffect(() => {
		fastWatchers.count += 1;
		void queryClient.invalidateQueries({ queryKey: sessionMemoryQueryRoot });
		return () => {
			fastWatchers.count -= 1;
		};
	}, [queryClient]);
}

export function useDiagnosticsEnabled(): boolean {
	return useUiStore((state) => state.developerMode && state.diagnostics);
}

function selectSessionReadings(data: SessionMemoryResponse) {
	return new Map(data.sessions.map((item) => [item.sessionId, item] as const));
}

function selectSystemReading(data: SessionMemoryResponse) {
	return data.system;
}

function selectAppReading(data: SessionMemoryResponse) {
	return {
		app: data.app,
		system: data.system,
		fetchedAt: data.fetchedAt,
		liveCount: data.sessions.length,
	};
}

export function useSessionMemory(projectId?: string, local = true) {
	const enabled = useDiagnosticsEnabled() && local;
	return useQuery({
		enabled,
		...sessionMemoryQueryOptions(projectId),
		select: selectSessionReadings,
	});
}

export function useSystemMemory(projectId?: string) {
	const enabled = useDiagnosticsEnabled();
	return useQuery({
		enabled,
		...sessionMemoryQueryOptions(projectId),
		select: selectSystemReading,
	});
}

export function useAppMemory(local = true) {
	const enabled = useDiagnosticsEnabled() && local;
	return useQuery({
		enabled,
		...sessionMemoryQueryOptions(),
		select: selectAppReading,
	});
}

export const memoryPressureRefetchIntervalMs = 10_000;
export const memoryPressureQueryKey = ["memory-pressure"] as const;

export function useMemoryPressure(local = true) {
	const enabled = useDiagnosticsEnabled() && local;
	return useQuery({
		enabled,
		queryKey: memoryPressureQueryKey,
		queryFn: async (): Promise<MemoryPressureReading> => {
			const { data, error } = await apiClient.GET("/api/v1/usage/memory/pressure");
			if (error) throw error;
			return data;
		},
		refetchInterval: memoryPressureRefetchIntervalMs,
		retry: false,
	});
}

const lastPressure: { state?: PressureState } = {};

export function usePressureState(local = true): PressureState | undefined {
	const queryClient = useQueryClient();
	const light = useMemoryPressure(local).data;
	const system = useAppMemory(local).data?.system;
	const reading = light ?? system;
	const state = reading ? pressureStateFromRaw(reading.pressureRaw, reading.pressureSource) : undefined;
	useEffect(() => {
		if (!state) return;
		if (lastPressure.state !== undefined && lastPressure.state !== state) {
			void queryClient.invalidateQueries({ queryKey: sessionMemoryQueryRoot });
		}
		lastPressure.state = state;
	}, [state, queryClient]);
	return state;
}

export const sampleHistoryLength = 60;

export type CPUSample = { host: number; ao: number };

export function cpuSampleOf(system?: SystemMemoryReading, app?: AppMemoryReading): CPUSample | undefined {
	if (!system || !app || !system.cpuMeasured || !app.cpuMeasured) return undefined;
	return { host: system.cpuPercent, ao: Math.min(100, app.cpuPercent / Math.max(1, system.cpuCount)) };
}

const cpuHistory = { samples: [] as CPUSample[], listeners: new Set<() => void>() };

function recordCPU(reading: SessionMemoryResponse) {
	const sample = cpuSampleOf(reading.system, reading.app);
	if (!sample) return;
	cpuHistory.samples = [...cpuHistory.samples, sample].slice(-sampleHistoryLength);
	for (const listener of cpuHistory.listeners) listener();
}

function subscribeCPUHistory(listener: () => void) {
	cpuHistory.listeners.add(listener);
	return () => {
		cpuHistory.listeners.delete(listener);
	};
}

export function useCPUHistory(): CPUSample[] {
	return useSyncExternalStore(subscribeCPUHistory, () => cpuHistory.samples);
}

export const formatMemory = formatResourceBytes;

export function formatCPU(percent: number): string {
	return `${Math.round(percent)}%`;
}
