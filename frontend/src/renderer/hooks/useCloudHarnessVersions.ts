import { queryOptions, useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import { useTranslation } from "react-i18next";
import type { CloudCpClient, CloudCpHarnessVersion, CloudCpHarnessVersionsResponse } from "../lib/cloud-cp";
import { CloudCpError } from "../lib/cloud-cp";
import type { AgentId } from "../lib/agent-options";
import { useCloudCp } from "./useCloudCp";
import { useCloudGate } from "./useCloudGate";
import { useCloudOrg } from "./useCloudOrg";
import { hasConfirmedHarnessUpdate, isFailedInstallJob, maintenanceMethodId, updateAdvisoryRefreshInterval } from "./useHarnessUpdates";

export const CLOUD_HARNESS_HOST = "ao-cloud";

export const CLOUD_VERSIONED_HARNESSES: ReadonlySet<AgentId> = new Set<AgentId>(["claude-code", "codex", "cursor"]);

export function cloudHarnessVersionsQueryKey(baseUrl: string, userId: string, orgId: string) {
	return ["cloud-harness-versions", baseUrl, userId, orgId] as const;
}

function cloudHarnessVersionsQueryOptions(scope: { client: CloudCpClient; baseUrl: string; userId: string; orgId: string; enabled: boolean }) {
	return queryOptions<CloudCpHarnessVersionsResponse | null>({
		queryKey: cloudHarnessVersionsQueryKey(scope.baseUrl, scope.userId, scope.orgId),
		enabled: scope.enabled,
		staleTime: (query) => updateAdvisoryRefreshInterval(query.state.status === "error"),
		refetchInterval: (query) => updateAdvisoryRefreshInterval(query.state.status === "error"),
		refetchIntervalInBackground: true,
		retry: false,
		queryFn: async ({ signal }): Promise<CloudCpHarnessVersionsResponse | null> => {
			try {
				return await scope.client.listOrgHarnessVersions(scope.orgId, undefined, { signal });
			} catch (error) {
				if (error instanceof CloudCpError && (error.status === 404 || error.status === 405)) return null;
				throw error;
			}
		},
	});
}

export interface CloudHarnessVersions {
	expected: boolean;
	data: CloudCpHarnessVersionsResponse | null | undefined;
	isPending: boolean;
	isError: boolean;
	startUpdate: (agentId: AgentId) => Promise<CloudHarnessUpdateStart>;
	resetVersion: (agentId: AgentId) => Promise<CloudHarnessUpdateStart>;
}

export type CloudHarnessUpdateStart = { entry?: CloudCpHarnessVersion; error?: string };

export function useCloudHarnessVersions(enabled = true): CloudHarnessVersions {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const { cloudEnabled } = useCloudGate(enabled);
	const { client, ready, baseUrl, userId } = useCloudCp(enabled && cloudEnabled);
	const { org } = useCloudOrg(enabled && cloudEnabled);
	const orgId = org?.id ?? "";
	const expected = enabled && cloudEnabled && ready && orgId !== "";
	const query = useQuery(cloudHarnessVersionsQueryOptions({ client, baseUrl, userId, orgId, enabled: expected }));

	const startUpdate = useCallback(async (agentId: AgentId): Promise<CloudHarnessUpdateStart> => {
		if (!expected) return {};
		const key = cloudHarnessVersionsQueryKey(baseUrl, userId, orgId);
		try {
			await queryClient.cancelQueries({ queryKey: key });
			const fresh = await client.listOrgHarnessVersions(orgId, { refresh: true });
			queryClient.setQueryData(key, fresh);
			const entry = fresh.agents.find((candidate) => candidate.agentId === agentId);
			if (!entry || !describeCloudHarness(fresh, agentId)?.actionable) return {};
			const updated = await client.installOrgHarnessVersion(orgId, agentId, entry.advisory.latestVersion!);
			setCloudHarnessEntry(queryClient, key, updated);
			return { entry: updated };
		} catch (error) {
			return { error: error instanceof Error ? error.message : t("settings.harness.updateFailed") };
		}
	}, [baseUrl, client, expected, orgId, queryClient, t, userId]);

	const resetVersion = useCallback(async (agentId: AgentId): Promise<CloudHarnessUpdateStart> => {
		if (!expected) return {};
		try {
			const updated = await client.resetOrgHarnessVersion(orgId, agentId);
			setCloudHarnessEntry(queryClient, cloudHarnessVersionsQueryKey(baseUrl, userId, orgId), updated);
			return { entry: updated };
		} catch (error) {
			return { error: error instanceof Error ? error.message : t("settings.harness.cloud.resetFailed") };
		}
	}, [baseUrl, client, expected, orgId, queryClient, t, userId]);

	return { expected, data: query.data, isPending: expected && query.isPending, isError: query.isError, startUpdate, resetVersion };
}

function setCloudHarnessEntry(queryClient: QueryClient, key: readonly unknown[], entry: CloudCpHarnessVersion) {
	queryClient.setQueryData<CloudCpHarnessVersionsResponse | null>(key, (current) => current ? {
		...current,
		agents: current.agents.map((candidate) => candidate.agentId === entry.agentId ? entry : candidate),
	} : current);
}

export interface CloudHarnessDetails {
	entry: CloudCpHarnessVersion;
	editable: boolean;
	currentVersion: string;
	latestVersion?: string;
	defaultVersion: string;
	onDefault: boolean;
	updateExists: boolean;
	actionable: boolean;
	failed: boolean;
	adapterVersion?: string;
}

export function describeCloudHarness(data: CloudCpHarnessVersionsResponse | null | undefined, agentId: AgentId): CloudHarnessDetails | undefined {
	const entry = data?.agents.find((candidate) => candidate.agentId === agentId);
	if (!data || !entry) return undefined;
	const method = entry.plan.methods.find((candidate) => candidate.id === maintenanceMethodId(entry.advisory));
	const updateExists = hasConfirmedHarnessUpdate(entry.advisory);
	const canUpdate = data.editable && Boolean(method?.available && method.updateAvailable);
	return {
		entry,
		editable: data.editable,
		currentVersion: entry.advisory.currentVersion?.trim() || entry.selectedVersion || entry.defaultVersion,
		latestVersion: entry.advisory.latestVersion?.trim() || undefined,
		defaultVersion: entry.defaultVersion,
		onDefault: !entry.selectedVersion,
		updateExists,
		actionable: updateExists && canUpdate,
		failed: isFailedInstallJob(entry.job),
		adapterVersion: entry.components?.find((component) => component.tool === "claude-agent-acp")?.version,
	};
}
