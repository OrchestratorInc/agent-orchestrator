/**
 * AO's own disk usage and its cleanup, read from and run by the daemon.
 *
 * The daemon measures in the background; the report says when it is still
 * measuring or cleaning, and the query polls quickly only then.
 */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";

export type StorageReport = components["schemas"]["StorageReportResponse"];
export type StorageSettings = components["schemas"]["StorageSettings"];
export type StorageCleanKind = components["schemas"]["StoragePlannedAction"]["kind"];
export type StorageCleanRequest = components["schemas"]["StorageCleanRequest"];
export type StorageCleanResult = components["schemas"]["StorageCleanResponse"];
export type StorageSettingsPatch = components["schemas"]["UpdateStorageSettingsRequest"];

export const storageQueryKey = ["storage"] as const;

const storageReadTimeoutMs = 15_000;
const busyPollMs = 2_000;
const idlePollMs = 60_000;

export function useStorageReport(enabled = true) {
	const query = useQuery({
		queryKey: storageQueryKey,
		enabled,
		refetchInterval: (q) => (q.state.data?.scanning || q.state.data?.cleaning ? busyPollMs : idlePollMs),
		queryFn: async ({ signal }): Promise<StorageReport> => {
			const { data, error } = await apiClient.GET("/api/v1/storage", {
				signal: AbortSignal.any([signal, AbortSignal.timeout(storageReadTimeoutMs)]),
			});
			if (error) throw error;
			if (!data) throw new Error("The daemon returned no storage report");
			return data;
		},
	});
	return {
		report: query.data,
		isLoading: query.isLoading,
		error: query.error ? apiErrorMessage(query.error) : undefined,
	};
}

export function useStorageScan() {
	const queryClient = useQueryClient();
	const mutation = useMutation({
		mutationFn: async () => {
			const { data, error } = await apiClient.POST("/api/v1/storage/scan", {});
			if (error) throw error;
			return data;
		},
		onSuccess: (data) => queryClient.setQueryData(storageQueryKey, data),
	});
	return { scan: () => mutation.mutate(), scanning: mutation.isPending };
}

/** Previews (dryRun) or starts a cleanup; a started one is followed through the report. */
export function useStorageClean() {
	const queryClient = useQueryClient();
	const mutation = useMutation({
		mutationFn: async (body: StorageCleanRequest): Promise<StorageCleanResult> => {
			const { data, error } = await apiClient.POST("/api/v1/storage/clean", { body });
			if (error) throw error;
			if (!data) throw new Error("The daemon returned no cleanup result");
			return data;
		},
		onSuccess: (_data, body) => {
			if (!body.dryRun) void queryClient.invalidateQueries({ queryKey: storageQueryKey });
		},
	});
	return {
		clean: (body: StorageCleanRequest) => mutation.mutateAsync(body),
		pending: mutation.isPending,
		error: mutation.error ? apiErrorMessage(mutation.error) : undefined,
		reset: () => mutation.reset(),
	};
}

export function useUpdateStorageSettings() {
	const queryClient = useQueryClient();
	const mutation = useMutation({
		mutationFn: async (body: StorageSettingsPatch) => {
			const { data, error } = await apiClient.PATCH("/api/v1/storage/settings", { body });
			if (error) throw error;
			return data;
		},
		// The daemon is the source of truth: refetch instead of assuming the write.
		onSuccess: () => queryClient.invalidateQueries({ queryKey: storageQueryKey }),
	});
	return {
		update: (body: StorageSettingsPatch) => mutation.mutate(body),
		saving: mutation.isPending,
		error: mutation.error ? apiErrorMessage(mutation.error) : undefined,
	};
}
