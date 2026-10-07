import { queryOptions, useQueries } from "@tanstack/react-query";
import { create } from "zustand";
import type { components } from "../../api/schema";
import { AGENT_OPTIONS, type AgentId } from "../lib/agent-options";
import { apiErrorMessage } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";
import { LOCAL_HOST } from "../lib/hosts";

export type AgentUpdateAdvisory = components["schemas"]["AgentUpdateAdvisory"];
export type HarnessInstallJob = components["schemas"]["InstallJob"];
export const installerQueryKey = ["agent-installers"] as const;
export const installJobsQueryKey = ["agent-install-jobs"] as const;
export const updateAdvisoryQueryKey = (agentId: AgentId, hostId?: string) => ["agent-update-advisory", hostId ?? LOCAL_HOST, agentId] as const;

export async function fetchInstallers(hostId?: string): Promise<components["schemas"]["AgentInstallPlan"][]> {
	const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/agents/installers");
	if (error || !data) throw new Error(apiErrorMessage(error, "Could not load harness installers."));
	return data.agents;
}

export async function fetchInstallJobs(hostId?: string): Promise<HarnessInstallJob[]> {
	const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/agents/install-jobs");
	if (error || !data) throw new Error(apiErrorMessage(error, "Could not load harness installation jobs."));
	return data.jobs;
}

export function hasDefinitiveUpdateStatus(advisory?: AgentUpdateAdvisory): boolean {
	return advisory?.status === "current" || hasConfirmedHarnessUpdate(advisory);
}

export function hasConfirmedHarnessUpdate(advisory?: AgentUpdateAdvisory): boolean {
	return advisory?.status === "behind_latest" && Boolean(advisory.currentVersion?.trim())
		&& Boolean(advisory.latestVersion?.trim()) && advisory.currentVersion?.trim() !== advisory.latestVersion?.trim();
}

export function updateAdvisoryRefreshInterval(advisory?: AgentUpdateAdvisory, hasError = false): number {
	return !hasError && hasDefinitiveUpdateStatus(advisory) ? 60 * 60_000 : 5 * 60_000;
}

export function useHarnessUpdates(installed: ReadonlySet<AgentId>, hostId?: string, enabled = true) {
	const queries = useQueries({ queries: AGENT_OPTIONS.map((agentId) => queryOptions({
		queryKey: updateAdvisoryQueryKey(agentId, hostId),
		queryFn: () => fetchUpdateAdvisory(agentId, hostId),
		enabled: enabled && installed.has(agentId),
		staleTime: (query) => updateAdvisoryRefreshInterval(query.state.data, query.state.status === "error"),
		refetchInterval: (query) => updateAdvisoryRefreshInterval(query.state.data, query.state.status === "error"),
		retry: false,
	})) });
	return new Map(AGENT_OPTIONS.map((agentId, index) => [agentId, queries[index]] as const));
}

export async function fetchUpdateAdvisory(agentId: AgentId, hostId?: string, refresh = false): Promise<AgentUpdateAdvisory> {
	const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/agents/{agent}/update-advisory", { params: { path: { agent: agentId }, ...(refresh ? { query: { refresh: true } } : {}) } });
	if (error || !data) throw new Error(apiErrorMessage(error, "Could not check harness updates."));
	return data;
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

// A popup action is handed to the existing Harness workflow, not a second
// installer/auth implementation. It is consumed once after the page is ready.
type HarnessActionRequest = { agentId: AgentId; latestVersion: string; action: "login" | "update"; hostId?: string };
export const useHarnessActionRequest = create<{
	request: HarnessActionRequest | null;
	setRequest: (request: HarnessActionRequest | null) => void;
}>((set) => ({ request: null, setRequest: (request) => set({ request }) }));
