import { queryOptions, useQueries, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../api/schema";
import { appI18n } from "../i18n";
import { AGENT_OPTIONS, agentLabel, type AgentId } from "../lib/agent-options";
import { apiErrorMessage } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";
import { LOCAL_HOST } from "../lib/hosts";
import type { AgentAuthPlan } from "./useAgentAuth";
import type { AgentReadiness } from "./useAgentReadinessQuery";

export type AgentUpdateAdvisory = components["schemas"]["AgentUpdateAdvisory"];
export type InstallJob = components["schemas"]["InstallJob"];
export const installerQueryKey = ["agent-installers"] as const;
export const installJobsQueryKey = ["agent-install-jobs"] as const;
export const installerQueryKeyForHost = (hostId?: string) => hostId ? [...installerQueryKey, hostId] as const : installerQueryKey;
export const installJobsQueryKeyForHost = (hostId?: string) => hostId ? [...installJobsQueryKey, hostId] as const : installJobsQueryKey;
export const updateAdvisoryQueryKey = (agentId: AgentId, hostId?: string) => ["agent-update-advisory", hostId ?? LOCAL_HOST, agentId] as const;
const INSTALL_JOB_POLL_MS = 1_000;

export async function fetchInstallers(hostId?: string): Promise<components["schemas"]["AgentInstallPlan"][]> {
	const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/agents/installers");
	if (error || !data) throw new Error(apiErrorMessage(error, appI18n.t("settings.harness.loadFailed")));
	return data.agents;
}

export async function fetchInstallJobs(hostId?: string): Promise<InstallJob[]> {
	const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/agents/install-jobs");
	if (error || !data) throw new Error(apiErrorMessage(error, appI18n.t("settings.harness.loadFailed")));
	return data.jobs;
}

export function installersQueryOptions(hostId?: string) {
	return queryOptions({ queryKey: installerQueryKeyForHost(hostId), queryFn: () => fetchInstallers(hostId), staleTime: 60_000 });
}

/** One jobs query per host, shared by the notice and the Harness page; it polls only while a job runs. */
export function installJobsQueryOptions(hostId?: string) {
	return queryOptions({
		queryKey: installJobsQueryKeyForHost(hostId),
		queryFn: () => fetchInstallJobs(hostId),
		retry: false,
		refetchInterval: (query) => query.state.data?.some(isActiveInstallJob) ? INSTALL_JOB_POLL_MS : false,
	});
}

export function isActiveInstallJob(job?: Pick<InstallJob, "status">): boolean {
	return job?.status === "running" || job?.status === "installing" || job?.status === "verifying";
}

export function isFailedInstallJob(job?: Pick<InstallJob, "status">): boolean {
	return job?.status === "failed" || job?.status === "unsupported" || job?.status === "interrupted";
}

export function upsertInstallJob(current: InstallJob[] | undefined, next: InstallJob): InstallJob[] {
	return [...(current ?? []).filter((job) => job.target !== next.target), next];
}

export function installedAgentIds(readiness?: AgentReadiness): Set<AgentId> {
	return new Set((readiness?.agents ?? []).filter((agent) => agent.installation.state === "installed").map((agent) => agent.id as AgentId));
}

/** A harness that must sign in before AO updates it or loads its models. */
export function needsHarnessLogin(plan: Pick<AgentAuthPlan, "action"> | undefined, authState: string | undefined): boolean {
	return Boolean(plan && plan.action !== "instructions" && authState === "unauthorized");
}

export function hasDefinitiveUpdateStatus(advisory?: AgentUpdateAdvisory): boolean {
	return advisory?.status === "current" || advisory?.status === "ahead_of_latest" || hasConfirmedHarnessUpdate(advisory);
}

export function hasConfirmedHarnessUpdate(advisory?: AgentUpdateAdvisory): boolean {
	return advisory?.status === "behind_latest" && Boolean(advisory.currentVersion?.trim())
		&& Boolean(advisory.latestVersion?.trim()) && advisory.currentVersion?.trim() !== advisory.latestVersion?.trim();
}

// The daemon checks harnesses on its own schedule and serves cached results,
// so polling only reads that cache; it never starts a CLI probe by itself.
export function updateAdvisoryRefreshInterval(hasError = false): number {
	return hasError ? 5 * 60_000 : 15 * 60_000;
}

export function useHarnessUpdates(installed: ReadonlySet<AgentId>, hostId?: string, enabled = true) {
	const queryClient = useQueryClient();
	const queries = useQueries({ queries: AGENT_OPTIONS.map((agentId) => queryOptions({
		queryKey: updateAdvisoryQueryKey(agentId, hostId),
		queryFn: async () => syncInstallerMethods(queryClient, await fetchUpdateAdvisory(agentId, hostId), hostId),
		enabled: enabled && installed.has(agentId),
		staleTime: (query) => updateAdvisoryRefreshInterval(query.state.status === "error"),
		refetchInterval: (query) => updateAdvisoryRefreshInterval(query.state.status === "error"),
		// Keep the update badge current while the window is hidden or minimized.
		refetchIntervalInBackground: true,
		retry: false,
	})) });
	return new Map(AGENT_OPTIONS.map((agentId, index) => [agentId, queries[index]] as const));
}

/**
 * An advisory can confirm a package owner the daemon derived after the
 * installers list was loaded (e.g. a Homebrew formula outside the fixed
 * methods). Reload the methods then, so Update doesn't depend on which
 * request finished first.
 */
function syncInstallerMethods(queryClient: QueryClient, advisory: AgentUpdateAdvisory, hostId?: string): AgentUpdateAdvisory {
	const methodId = maintenanceMethodId(advisory);
	if (!methodId || !hasConfirmedHarnessUpdate(advisory)) return advisory;
	const plans = queryClient.getQueryData<components["schemas"]["AgentInstallPlan"][]>(installerQueryKeyForHost(hostId));
	const method = plans?.find((plan) => plan.agentId === advisory.agentId)?.methods.find((candidate) => candidate.id === methodId);
	if (!method?.available || !method.updateAvailable) void queryClient.invalidateQueries({ queryKey: installerQueryKeyForHost(hostId), exact: true });
	return advisory;
}

export async function fetchUpdateAdvisory(agentId: AgentId, hostId?: string, refresh = false): Promise<AgentUpdateAdvisory> {
	const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/agents/{agent}/update-advisory", { params: { path: { agent: agentId }, ...(refresh ? { query: { refresh: true } } : {}) } });
	if (error || !data) throw new Error(apiErrorMessage(error, appI18n.t("settings.harness.updateCheckUnavailable")));
	return data;
}

/** No job and no error: the click-time check found nothing left to install. */
export type HarnessUpdateStart = { job?: InstallJob; error?: string };

/**
 * Starts an update the user saw, shared by the notice and the Harness page.
 * The advisory is re-checked at click time, and only that release, through
 * that method, is installed. The started job lands in the shared jobs query.
 */
export function useStartHarnessUpdate(hostId?: string) {
	const queryClient = useQueryClient();
	const { t } = useTranslation();
	return useCallback(async ({ agentId, latestVersion, methodId, canStart }: {
		agentId: AgentId;
		latestVersion: string | undefined;
		methodId: string;
		/** Checked after the click-time probe, e.g. so an unmounted page starts nothing. */
		canStart?: () => boolean;
	}): Promise<HarnessUpdateStart> => {
		try {
			const key = updateAdvisoryQueryKey(agentId, hostId);
			await queryClient.cancelQueries({ queryKey: key });
			const fresh = syncInstallerMethods(queryClient, await fetchUpdateAdvisory(agentId, hostId, true), hostId);
			queryClient.setQueryData(key, fresh);
			// No longer behind: the refreshed advisory already shows why.
			if (!hasConfirmedHarnessUpdate(fresh) || canStart?.() === false) return {};
			if (fresh.latestVersion !== latestVersion) return { error: t("settings.harness.updateTargetChanged", { version: versionLabel(fresh.latestVersion!) }) };
			if (maintenanceMethodId(fresh) !== methodId) return { error: t("settings.harness.updateMethodChanged", { agent: agentLabel(agentId) }) };
			const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/agents/{agent}/install", {
				params: { path: { agent: agentId } },
				body: { method: methodId, operation: "update", expectedVersion: fresh.latestVersion },
			});
			if (error || !data) return { error: apiErrorMessage(error, t("settings.harness.updateFailed")) };
			queryClient.setQueryData<InstallJob[]>(installJobsQueryKeyForHost(hostId), (current) => upsertInstallJob(current, data));
			return { job: data };
		} catch (error) {
			return { error: error instanceof Error ? error.message : t("settings.harness.updateFailed") };
		}
	}, [hostId, queryClient, t]);
}

export function maintenanceMethodId(advisory?: AgentUpdateAdvisory): string {
	if (advisory?.reason === "ownership_unconfirmed") return "";
	return advisory?.maintenanceMethod
		?? (advisory?.source && advisory.source !== "official-release" ? advisory.source : "");
}

export function versionLabel(version: string): string {
	return !/^\d{4}[.-]\d{2}[.-]\d{2}/.test(version) && /^\d+\.\d+\.\d+(?:[-+].*)?$/.test(version) ? `v${version}` : version;
}

export function harnessUpdateNoticeKey(agentId: AgentId, latestVersion: string, hostId = LOCAL_HOST): string {
	return JSON.stringify([hostId, agentId, latestVersion]);
}
