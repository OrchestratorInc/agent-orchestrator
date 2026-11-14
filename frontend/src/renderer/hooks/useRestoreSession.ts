import { useCallback } from "react";
import { type QueryClient, useQueryClient } from "@tanstack/react-query";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { clientForHost } from "../lib/host-clients";
import { aoBridge } from "../lib/bridge";
import type { CloudCpSession } from "../lib/cloud-cp";
import { createRendererCloudCpClient } from "./useCloudCp";
import { settingsQueryKey, type Settings } from "./useSettings";
import { cloudSessionsQueryKey, workspaceQueryKey, workspaceQueryKeyForHost } from "./useWorkspaceQuery";
import { useTerminalResetStore } from "../stores/terminal-reset-store";
import type { WorkspaceSummary } from "../types/workspace";
import { recordDirectWorkerInteraction } from "../lib/session-management-telemetry";

export type RestoreSessionResult =
	{ status: "success" } | { status: "not_resumable"; message: string } | { status: "error"; message: string };

function findCloudSession(
	queryClient: QueryClient,
	sessionId: string,
): { orgId: string; workerEpoch: number; kind: "orchestrator" | "worker" } | undefined {
	for (const [key, sessions] of queryClient.getQueriesData<CloudCpSession[]>({ queryKey: cloudSessionsQueryKey })) {
		const session = sessions?.find((entry) => entry.id === sessionId);
		if (session) {
			const orgId = key[2];
			if (typeof orgId === "string" && orgId !== "") {
				if (session.kind !== "orchestrator" && session.kind !== "worker") continue;
				return { orgId, workerEpoch: session.workerEpoch ?? 0, kind: session.kind };
			}
		}
	}
	return undefined;
}

export function useRestoreSession(): (sessionId: string, hostId?: string) => Promise<RestoreSessionResult> {
	const queryClient = useQueryClient();

	return useCallback(
		async (sessionId: string, hostId?: string) => {
			const cloudSession = hostId ? undefined : findCloudSession(queryClient, sessionId);
			const role = queryClient.getQueryData<WorkspaceSummary[]>(workspaceQueryKeyForHost(hostId))
				?.flatMap((workspace) => workspace.sessions)
				.find((session) => session.id === sessionId)?.kind;
			recordDirectWorkerInteraction(sessionId, "lifecycle", role ?? cloudSession?.kind, hostId);
			if (cloudSession !== undefined) {
				const { orgId: cloudOrgId, workerEpoch: baselineEpoch } = cloudSession;
				const settings = queryClient.getQueryData<Settings>(settingsQueryKey);
				const baseUrl = settings?.cloudControlPlaneUrl ?? "";
				if (baseUrl === "") {
					return { status: "error", message: "The cloud control plane is not configured." };
				}
				try {
					await createRendererCloudCpClient(baseUrl).restoreSession(cloudOrgId, sessionId);
					await queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey });
					await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
					useTerminalResetStore.getState().bump(sessionId, baselineEpoch);
					return { status: "success" };
				} catch (err) {
					return {
						status: "error",
						message: err instanceof Error ? err.message : "Unable to restore session",
					};
				}
			}

			try {
				const { data, error } = await (hostId ? clientForHost(hostId) : apiClient).POST("/api/v1/sessions/{sessionId}/restore", {
					params: { path: { sessionId } },
				});
				if (error) {
					const code = (error as { code?: string }).code;
					const message = apiErrorMessage(error, "Unable to restore session");
					if (code === "SESSION_NOT_RESUMABLE") {
						return { status: "not_resumable", message };
					}
					return { status: "error", message };
				}
				await queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) });
				if (data?.restoreMode === "saved_prompt") {
					void aoBridge.notifications
						.show({
							id: `restore-fallback:${hostId ?? "local"}:${sessionId}:${Date.now()}`,
							title: "Started from saved prompt",
							body: "Orchestrator could not resume the native agent session, so it started a new conversation from the saved prompt.",
						})
						.catch((err) => {
							console.warn("Unable to show restore fallback notification", err);
						});
				}
				return { status: "success" };
			} catch (err) {
				return {
					status: "error",
					message: err instanceof Error ? err.message : "Unable to restore session",
				};
			}
		},
		[queryClient],
	);
}
