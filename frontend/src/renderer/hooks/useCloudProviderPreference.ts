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
const preferenceOperations = new WeakMap<ReturnType<typeof useQueryClient>, Map<string, number>>();

function nextOperation(queryClient: ReturnType<typeof useQueryClient>, identity: string): number {
	let operations = preferenceOperations.get(queryClient);
	if (!operations) {
		operations = new Map();
		preferenceOperations.set(queryClient, operations);
	}
	const next = (operations.get(identity) ?? 0) + 1;
	operations.set(identity, next);
	return next;
}

function isLatestOperation(queryClient: ReturnType<typeof useQueryClient>, identity: string, operation: number): boolean {
	return preferenceOperations.get(queryClient)?.get(identity) === operation;
}

/** Seed the old per-machine choice only when no account preference wins. */
export async function migrateLegacyCloudProvider(
	client: PreferenceClient,
	current: string | null,
	available: string[],
	isCurrent: () => boolean = () => true,
): Promise<string | null> {
	if (!isCurrent()) throw new Error("Cloud account changed while syncing provider preference.");
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
		if (!isCurrent()) throw new Error("Cloud account changed while syncing provider preference.");
		clearLegacySandboxProvider();
		return saved.sandboxProvider;
	} catch (error) {
		if (typeof error === "object" && error !== null && "code" in error && error.code === "preference_conflict") {
			if (!isCurrent()) throw new Error("Cloud account changed while syncing provider preference.");
			const winner = await client.getUserPreferences();
			if (!isCurrent()) throw new Error("Cloud account changed while syncing provider preference.");
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
	const migrationErrorKey = [...queryKey, "migration-error"] as const;
	const enabled = ready && userId !== "";
	const query = useQuery({
		queryKey,
		enabled,
		queryFn: async ({ signal }) => (await client.getUserPreferences({ signal })).sandboxProvider,
		staleTime: 60_000,
		retry: 1,
	});
	const migrationError = useQuery({
		queryKey: migrationErrorKey,
		enabled: false,
		initialData: null as string | null,
	});
	const [saving, setSaving] = useState(false);
	const [saveError, setSaveError] = useState<string | null>(null);
	const attemptedMigration = useRef(new Set<string>());
	const identity = `${baseUrl}\u0000${userId}`;
	const runIdentity = `${identity}\u0000${session?.authProvider ?? ""}\u0000${session?.storedAt ?? ""}`;
	const activeIdentity = useRef("");
	activeIdentity.current = status === "authenticated" ? runIdentity : "";
	const isCurrent = () => activeIdentity.current === runIdentity;

	useEffect(() => {
		setSaving(false);
		setSaveError(null);
	}, [identity]);

	useEffect(() => {
		if (!migrateLegacy || !enabled || !query.isSuccess || !providersLoaded) return;
		if (attemptedMigration.current.has(runIdentity)) return;
		attemptedMigration.current.add(runIdentity);
		const operation = nextOperation(queryClient, identity);
		void migrateLegacyCloudProvider(client, query.data, available, isCurrent)
			.then((provider) => {
				if (!isCurrent()) {
					attemptedMigration.current.delete(runIdentity);
					return;
				}
				if (isLatestOperation(queryClient, identity, operation)) queryClient.setQueryData(queryKey, provider);
				queryClient.setQueryData(migrationErrorKey, null);
			})
			.catch((error: unknown) => {
				if (isCurrent()) {
					queryClient.setQueryData(migrationErrorKey, error instanceof Error ? error.message : "Could not sync the Cloud provider preference.");
				}
			});
	}, [migrateLegacy, enabled, query.isSuccess, query.data, providersLoaded, available, identity, runIdentity, client, queryClient]);

	const setProvider = useCallback(async (provider: string | null) => {
		if (!enabled) return;
		const operation = nextOperation(queryClient, identity);
		setSaving(true);
		setSaveError(null);
		queryClient.setQueryData(migrationErrorKey, null);
		try {
			await queryClient.cancelQueries({ queryKey, exact: true });
			if (!isCurrent()) return;
			const saved = await client.putUserPreferences({ sandboxProvider: provider });
			if (isCurrent() && isLatestOperation(queryClient, identity, operation)) queryClient.setQueryData(queryKey, saved.sandboxProvider);
		} catch (error) {
			if (isCurrent()) {
				setSaveError(error instanceof Error ? error.message : "Could not save the Cloud provider preference.");
			}
		} finally {
			if (isCurrent()) setSaving(false);
		}
	}, [enabled, client, queryClient, identity, runIdentity]);

	return {
		provider: enabled ? (query.data ?? null) : null,
		loading: status === "loading" || (status === "authenticated" && (!enabled || query.isLoading)),
		saving,
		error: enabled ? (saveError ?? migrationError.data ?? (query.error instanceof Error ? query.error.message : null)) : null,
		setProvider,
	};
}
