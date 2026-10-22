import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { buildRankedAgentOptions, DEFAULT_AGENT_PRIORITY_RANK, isReadyAgent } from "../lib/agent-select-options";
import { useNavigateToSession } from "../lib/navigate-to-session";
import { captureRendererEvent } from "../lib/telemetry";
import { useUiStore } from "../stores/ui-store";
import { useAgentReadinessQuery } from "./useAgentReadinessQuery";
import { workspaceQueryKey } from "./useWorkspaceQuery";

type Harness = components["schemas"]["SpawnSessionRequest"]["harness"];
type ProjectAgents = { agent?: string; config?: { worker?: { agent?: string } } };

/**
 * Starts a worker session whose output kind is artifact, in a project or the
 * Scratchpad (no projectId), and opens it. The agent is the project's worker
 * agent, then the daemon-wide default, then the first ready agent.
 */
export function useCreateArtifactSession() {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const navigateToSession = useNavigateToSession();
	const agentsQuery = useAgentReadinessQuery();
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
		async (projectId?: string) => {
			const scope = projectId ? "project" : "standalone";
			setPending(true);
			void captureRendererEvent("ao.renderer.artifact_create_requested", { scope });
			try {
				const harness = projectId ? (await projectWorkerAgent(queryClient, projectId)) || readyAgent : readyAgent;
				const { data, error } = await apiClient.POST("/api/v1/sessions", {
					body: {
						kind: "worker",
						requestedOutput: "artifact",
						displayName: t("artifacts.newSessionName"),
						...(projectId ? { projectId } : {}),
						...(harness ? { harness: harness as Harness } : {}),
					},
				});
				if (error) throw new Error(apiErrorMessage(error, t("artifacts.createError")));
				if (!data?.session.id) throw new Error(t("artifacts.createError"));
				void captureRendererEvent("ao.renderer.artifact_create_succeeded", { scope });
				await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
				navigateToSession(projectId, data.session.id);
			} catch (error) {
				void captureRendererEvent("ao.renderer.artifact_create_failed", { scope });
				useUiStore
					.getState()
					.showGlobalToast(error instanceof Error ? error.message : t("artifacts.createError"), undefined, "error");
			} finally {
				setPending(false);
			}
		},
		[navigateToSession, queryClient, readyAgent, t],
	);

	return {
		create,
		pending,
		/** False once readiness has loaded and no agent can launch. */
		agentReady: agentsQuery.data === undefined || readyAgent !== "",
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
			if (error) throw new Error(apiErrorMessage(error));
			if (data?.status !== "ok") throw new Error("Project config unavailable");
			return data.project as ProjectAgents;
		},
	});
	return project.config?.worker?.agent || project.agent || "";
}
