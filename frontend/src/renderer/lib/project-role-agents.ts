import type { QueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { loadProjectAgentDefaults } from "../hooks/useAgentReadinessQuery";
import { workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { apiClient } from "./api-client";
import { apiErrorException } from "./api-error-exception";
import { clientForHost } from "./host-clients";

type RoleAgent = components["schemas"]["RoleOverride"]["agent"];

/**
 * Gives a project whose worker or orchestrator role names no agent the ranked
 * default agent, saved to its config, so starting its orchestrator works
 * instead of sending the user to settings. Roles that name an agent are never
 * changed. It re-reads the project right before the whole-config save so no
 * other setting is lost, and does the fill here rather than relying on the
 * daemon so a remote host on an older version heals too.
 */
export async function fillMissingProjectRoleAgents(queryClient: QueryClient, projectId: string, hostId?: string): Promise<void> {
	const client = hostId ? clientForHost(hostId) : apiClient;
	const current = await client.GET("/api/v1/projects/{id}", { params: { path: { id: projectId } } });
	if (current.error) throw apiErrorException(current.error);
	if (current.data?.status !== "ok" || !current.data.project) return;
	const project = current.data.project;
	const config = structuredClone(project.config ?? {});
	if (config.worker?.agent && config.orchestrator?.agent) return;
	const defaults = await loadProjectAgentDefaults(queryClient, hostId);
	config.worker = { ...config.worker, agent: config.worker?.agent || defaults.workerAgent as RoleAgent };
	config.orchestrator = { ...config.orchestrator, agent: config.orchestrator?.agent || defaults.orchestratorAgent as RoleAgent };
	const { error } = await client.PUT("/api/v1/projects/{id}", {
		params: { path: { id: projectId } },
		body: { displayName: project.name, config },
	});
	if (error) throw apiErrorException(error);
	void queryClient.invalidateQueries({ queryKey: hostId ? ["project", hostId, projectId] : ["project", projectId] });
	void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) });
}
