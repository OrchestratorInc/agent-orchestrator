import { apiErrorException } from "../lib/api-error-exception";
import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../api/schema";
import { apiClient, apiErrorCode } from "../lib/api-client";
import { buildRankedAgentOptions, DEFAULT_AGENT_PRIORITY_RANK, isReadyAgent } from "../lib/agent-select-options";
import { cloudDefaultInterfaceMode, createCloudSession } from "../lib/cloud-session-create";
import { useNavigateToSession } from "../lib/navigate-to-session";
import { captureRendererEvent } from "../lib/telemetry";
import { cloudErrorReference, failureTelemetry } from "../lib/error-origin";
import { useUiStore } from "../stores/ui-store";
import { useAgentReadinessQuery } from "./useAgentReadinessQuery";
import { useCloudCp } from "./useCloudCp";
import { useSettings } from "./useSettings";
import { cloudSessionsQueryKey, workspaceQueryKey } from "./useWorkspaceQuery";

type Harness = components["schemas"]["SpawnSessionRequest"]["harness"];
type ProjectAgents = { agent?: string; config?: { worker?: { agent?: string } } };

/**
 * Starts a worker session whose output kind is artifact, in a project or the
 * Scratchpad (no projectId), and opens it. The agent is the project's worker
 * agent, then the daemon-wide default, then the first ready agent. A cloud
 * project (cloudOrgId set) starts the session on the control plane with the
 * project's worker agent, else Claude Code.
 */
export function useCreateArtifactSession() {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const { client: cloudClient } = useCloudCp();
	const navigateToSession = useNavigateToSession();
	const agentsQuery = useAgentReadinessQuery();
	const { settings } = useSettings();
	const [pending, setPending] = useState(false);
	const readyAgent = useMemo(
		() =>
			buildRankedAgentOptions({
				agents: agentsQuery.data?.agents,
				priorityRank: DEFAULT_AGENT_PRIORITY_RANK,
				fallbackAgents: [],
			}).find(isReadyAgent)?.id ?? "",
		[agentsQuery.data],
	);

	const create = useCallback(
		async (projectId?: string, cloudOrgId?: string) => {
			const scope = cloudOrgId ? "cloud" : projectId ? "project" : "standalone";
			// The daemon's answer to a refused start, for its telemetry.
			let refusedStatus: number | undefined;
			setPending(true);
			try {
				if (cloudOrgId && projectId) {
					const { project } = await cloudClient.getProject(cloudOrgId, projectId);
					// Started like a task: in the user's default session interface,
					// through createCloudSession, which retries without it for a
					// control plane that predates the field.
					const { session } = await createCloudSession(cloudClient, cloudOrgId, {
						projectId,
						kind: "worker",
						harness: project.config?.worker?.agent || "claude-code",
						displayName: t("artifacts.newSessionName"),
						prompt: "",
						requestedOutput: "artifact",
						defaultInterfaceMode: cloudDefaultInterfaceMode(settings),
					});
					void captureRendererEvent("ao.artifacts.session_started", { scope });
					await queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey });
					navigateToSession(projectId, session.id);
					return;
				}
				const harness = projectId ? (await projectWorkerAgent(queryClient, projectId)) || readyAgent : readyAgent;
				const { data, error, response } = await apiClient.POST("/api/v1/sessions", {
					body: {
						kind: "worker",
						requestedOutput: "artifact",
						displayName: t("artifacts.newSessionName"),
						...(projectId ? { projectId } : {}),
						...(harness ? { harness: harness as Harness } : {}),
					},
				});
				if (error) {
					refusedStatus = response?.status;
					throw apiErrorException(error, t("artifacts.createError"));
				}
				if (!data?.session.id) throw new Error(t("artifacts.createError"));
				void captureRendererEvent("ao.artifacts.session_started", { scope });
				await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
				navigateToSession(projectId, data.session.id);
			} catch (error) {
				const { source } = failureTelemetry(error, cloudOrgId ? "cloud" : "daemon");
				void captureRendererEvent("ao.artifacts.session_start_failed", { scope, ...(source ? { source } : {}), ...startFailure(error, refusedStatus) });
				const reference = cloudErrorReference(error);
				useUiStore
					.getState()
					.showGlobalToast(error instanceof Error ? error.message : t("artifacts.createError"), undefined, reference ? { tone: "error", reference } : "error");
			} finally {
				setPending(false);
			}
		},
		[cloudClient, navigateToSession, queryClient, readyAgent, settings, t],
	);

	return {
		create,
		pending,
		/** False once readiness has loaded and no agent can launch. */
		agentReady: agentsQuery.data === undefined || readyAgent !== "",
	};
}

/**
 * The error code and HTTP status of a start that failed, never its message:
 * a daemon refusal's code is on the error the exception wraps, a control
 * plane's on the error itself.
 */
function startFailure(error: unknown, refusedStatus?: number): { error_code?: string; http_status?: number } {
	const cause = error instanceof Error ? error.cause : undefined;
	const code = apiErrorCode(cause) ?? apiErrorCode(error);
	const status = refusedStatus ?? (error as { status?: unknown } | null)?.status;
	return {
		...(code ? { error_code: code } : {}),
		...(typeof status === "number" ? { http_status: status } : {}),
	};
}

// Shares the ["project", id] cache entry with useProjectDefaultWorker.
async function projectWorkerAgent(queryClient: ReturnType<typeof useQueryClient>, projectId: string): Promise<string> {
	const project = await queryClient.fetchQuery({
		queryKey: ["project", projectId],
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/projects/{id}", {
				params: { path: { id: projectId } },
			});
			if (error) throw apiErrorException(error);
			if (data?.status !== "ok") throw new Error("Project config unavailable");
			return data.project as ProjectAgents;
		},
	});
	return project.config?.worker?.agent || project.agent || "";
}
