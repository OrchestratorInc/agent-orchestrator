import { useCallback, useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import type { CloudCpClient } from "../lib/cloud-cp";
import { useCloudSession } from "../lib/cloud-session";
import { clearLegacySandboxProvider, readSelectedSandboxProvider } from "../stores/sandbox-provider-store";
import { useCloudCp } from "./useCloudCp";
import { useCloudSandboxProviders } from "./useCloudSandboxProviders";

export const cloudProviderPreferenceQueryKey = (baseUrl: string, userId: string) =>
	["cloud-provider-preference", baseUrl, userId] as const;

type PreferenceClient = Pick<CloudCpClient, "getUserPreferences" | "putUserPreferences">;

/** Seed the old per-machine choice only when no account preference wins. */
export async function migrateLegacyCloudProvider(
	client: PreferenceClient,
	current: string | null,
	available: string[],
): Promise<string | null> {
	if (current !== null) {
		clearLegacySandboxProvider();
		return current;
	}
	const legacy = readSelectedSandboxProvider();
	if (legacy === null || !available.includes(legacy)) {
		clearLegacySandboxProvider();
		return null;
	}
	try {
		const saved = await client.putUserPreferences({ sandboxProvider: legacy, initializeOnly: true });
		clearLegacySandboxProvider();
		return saved.sandboxProvider;
	} catch (error) {
		if (typeof error === "object" && error !== null && "code" in error && error.code === "preference_conflict") {
			const winner = await client.getUserPreferences();
			clearLegacySandboxProvider();
			return winner.sandboxProvider;
		}
		throw error;
	}
}

export function useCloudProviderPreference({ migrateLegacy = false }: { migrateLegacy?: boolean } = {}) {
	const { session, status } = useCloudSession();
	const { client, ready, baseUrl } = useCloudCp();
	const { available, isSuccess: providersLoaded } = useCloudSandboxProviders();
	const queryClient = useQueryClient();
	const userId = status === "authenticated" ? (session?.user.id ?? "") : "";
	const queryKey = cloudProviderPreferenceQueryKey(baseUrl, userId);
	const enabled = ready && userId !== "";
	const query = useQuery({
		queryKey,
		enabled,
		queryFn: async () => (await client.getUserPreferences()).sandboxProvider,
		staleTime: 60_000,
		retry: 1,
	});
	const [saving, setSaving] = useState(false);
	const [saveError, setSaveError] = useState<string | null>(null);
	const attemptedMigration = useRef(new Set<string>());
	const activeIdentity = useRef("");
	const identity = `${baseUrl}\u0000${userId}`;
	activeIdentity.current = identity;

	useEffect(() => {
		setSaving(false);
		setSaveError(null);
	}, [identity]);

	useEffect(() => {
		if (!migrateLegacy || !enabled || !query.isSuccess || !providersLoaded) return;
		if (attemptedMigration.current.has(identity)) return;
		attemptedMigration.current.add(identity);
		void migrateLegacyCloudProvider(client, query.data, available)
			.then((provider) => {
				queryClient.setQueryData(queryKey, provider);
				setSaveError(null);
			})
			.catch((error: unknown) => {
				attemptedMigration.current.delete(identity);
				if (activeIdentity.current === identity) {
					setSaveError(error instanceof Error ? error.message : "Could not sync the Cloud provider preference.");
				}
			});
	}, [migrateLegacy, enabled, query.isSuccess, query.data, providersLoaded, available, identity, client, queryClient, baseUrl, userId]);

	const setProvider = useCallback(async (provider: string | null) => {
		if (!enabled) return;
		setSaving(true);
		setSaveError(null);
		try {
			const saved = await client.putUserPreferences({ sandboxProvider: provider });
			queryClient.setQueryData(queryKey, saved.sandboxProvider);
		} catch (error) {
			if (activeIdentity.current === identity) {
				setSaveError(error instanceof Error ? error.message : "Could not save the Cloud provider preference.");
			}
		} finally {
			if (activeIdentity.current === identity) setSaving(false);
		}
	}, [enabled, client, queryClient, baseUrl, userId, identity]);

	return {
		provider: enabled ? (query.data ?? null) : null,
		loading: status === "loading" || (status === "authenticated" && (!enabled || query.isLoading)),
		saving,
		error: enabled ? (saveError ?? (query.error instanceof Error ? query.error.message : null)) : null,
		setProvider,
	};
}
