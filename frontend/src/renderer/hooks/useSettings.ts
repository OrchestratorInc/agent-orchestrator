import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { clientForHost } from "../lib/host-clients";
import type { SessionMode } from "../types/workspace";

export type HarnessDefault = Pick<components["schemas"]["HarnessDefault"], "disabledModels">;

export const settingsQueryKey = ["settings"] as const;

export interface Settings {
	harnessDefaults?: Record<string, HarnessDefault>;
	defaultSessionMode: SessionMode;
	chatHarnesses: string[];
	client: string;
	localEnabled: boolean;
	cloudEnabled: boolean;
	cloudControlPlaneUrl: string;
	trackerIntakeEnabled: boolean;
	versionRefresh?: boolean;
	computerUse?: boolean;
	sessionRetentionDays?: number;
	accountHandoff?: boolean;
}

const settingsReadTimeoutMs = 10_000;

export function useSettings(hostId?: string, enabled = true) {
	const query = useQuery({
		queryKey: hostId ? ["settings", hostId] : settingsQueryKey,
		enabled,
		refetchInterval: 15_000,
		retry: 5,
		queryFn: async ({ signal }): Promise<Settings> => {
			const { data, error } = await (hostId ? clientForHost(hostId) : apiClient).GET("/api/v1/settings", {
				signal: AbortSignal.any([signal, AbortSignal.timeout(settingsReadTimeoutMs)]),
			});
			if (error) throw error;
			return {
				defaultSessionMode: (data?.defaultSessionMode ?? "tui") as SessionMode,
				harnessDefaults: data?.harnessDefaults ?? {},
				chatHarnesses: data?.chatHarnesses ?? [],
				client: data?.client ?? "",
				localEnabled: data?.localEnabled ?? true,
				cloudEnabled: data?.cloudEnabled ?? false,
				cloudControlPlaneUrl: data?.cloudControlPlaneUrl ?? "",
				trackerIntakeEnabled: data?.trackerIntakeEnabled ?? false,
				versionRefresh: data?.versionRefresh ?? false,
				computerUse: data?.computerUse ?? false,
				sessionRetentionDays: data?.sessionRetentionDays,
				accountHandoff: data?.accountHandoff ?? false,
			};
		},
	});

	return {
		settings: query.data,
		isLoading: query.isLoading,
		error: query.error ? apiErrorMessage(query.error) : undefined,
	};
}

export function useUpdateSessionInterface() {
	const queryClient = useQueryClient();
	const mutation = useMutation({
		mutationFn: async (mode: SessionMode) => {
			const { data, error } = await apiClient.PATCH("/api/v1/settings/session-interface", {
				body: { defaultSessionMode: mode },
			});
			if (error) throw error;
			return data;
		},
		onSuccess: () => queryClient.invalidateQueries({ queryKey: settingsQueryKey }),
	});

	return {
		update: (mode: SessionMode) => mutation.mutate(mode),
		saving: mutation.isPending,
		error: mutation.error ? apiErrorMessage(mutation.error) : undefined,
	};
}

export function useUpdateHarnessDefault(hostId?: string) {
	const queryClient = useQueryClient();
	const key = hostId ? ["settings", hostId] : settingsQueryKey;
	const mutation = useMutation({
		mutationFn: async ({ agentId, disabledModels }: { agentId: string; disabledModels: string[] }) => {
			const { data, error } = await (hostId ? clientForHost(hostId) : apiClient).PATCH("/api/v1/settings/harness-defaults/{agent}", {
				params: { path: { agent: agentId } }, body: { disabledModels },
			});
			if (error) throw error;
			return data;
		},
		onSuccess: async (data) => {
			queryClient.setQueryData<Settings>(key, (current) => current ? { ...current, harnessDefaults: data?.harnessDefaults ?? {} } : current);
			await queryClient.invalidateQueries({ queryKey: key, exact: true });
		},
	});
	return { update: mutation.mutateAsync, error: mutation.error ? apiErrorMessage(mutation.error) : undefined };
}

export function useUpdateVersionRefresh() {
	const queryClient = useQueryClient();
	const mutation = useMutation({
		mutationFn: async (enabled: boolean) => {
			const { data, error } = await apiClient.PATCH("/api/v1/settings/version-refresh", {
				body: { enabled },
			});
			if (error) throw error;
			return data;
		},
		onSuccess: () => queryClient.invalidateQueries({ queryKey: settingsQueryKey }),
	});

	return {
		update: (enabled: boolean) => mutation.mutate(enabled),
		saving: mutation.isPending,
		error: mutation.error ? apiErrorMessage(mutation.error) : undefined,
	};
}

export function useUpdateSessionRetention() {
	const queryClient = useQueryClient();
	const mutation = useMutation({
		mutationFn: async (days: number) => {
			const { data, error } = await apiClient.PATCH("/api/v1/settings/session-retention", {
				body: { days },
			});
			if (error) throw error;
			return data;
		},
		onSuccess: () => queryClient.invalidateQueries({ queryKey: settingsQueryKey }),
	});

	return {
		update: (days: number) => mutation.mutate(days),
		saving: mutation.isPending,
		error: mutation.error ? apiErrorMessage(mutation.error) : undefined,
	};
}

export function useUpdateAccountHandoff() {
	const queryClient = useQueryClient();
	const mutation = useMutation({
		mutationFn: async (enabled: boolean) => {
			const { data, error } = await apiClient.PATCH("/api/v1/settings/account-handoff", {
				body: { enabled },
			});
			if (error) throw error;
			return data;
		},
		onSuccess: () => queryClient.invalidateQueries({ queryKey: settingsQueryKey }),
	});

	return {
		update: (enabled: boolean) => mutation.mutate(enabled),
		saving: mutation.isPending,
		error: mutation.error ? apiErrorMessage(mutation.error) : undefined,
	};
}

export function useUpdateComputerUse() {
	const queryClient = useQueryClient();
	const mutation = useMutation({
		mutationFn: async (enabled: boolean) => {
			const { data, error } = await apiClient.PATCH("/api/v1/settings/computer-use", {
				body: { enabled },
			});
			if (error) throw error;
			return data;
		},
		onSuccess: () => queryClient.invalidateQueries({ queryKey: settingsQueryKey }),
	});

	return {
		update: (enabled: boolean) => mutation.mutate(enabled),
		saving: mutation.isPending,
		error: mutation.error ? apiErrorMessage(mutation.error) : undefined,
	};
}
