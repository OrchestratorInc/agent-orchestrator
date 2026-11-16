import type { QueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { loadRankedProjectAgentDefaults } from "../hooks/useAgentReadinessQuery";
import { workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { apiClient } from "./api-client";
import { apiErrorException } from "./api-error-exception";
import { clientForHost } from "./host-clients";

type RoleAgent = components["schemas"]["RoleOverride"]["agent"];

type ProjectConfig = components["schemas"]["ProjectConfig"];

/**
 * Gives a project whose worker or orchestrator role names no agent the ranked
 * default agent, saved to its config, before its orchestrator starts. Only
 * remote hosts need it: a daemon running this version fills empty roles itself
 * when a session starts, under its own write lock, while an older host daemon
 * refuses the start instead. It costs one project read when both roles are set,
 * so callers can run it before every remote start.
 *
 * Roles that name an agent are never changed. Nothing is written when agent
 * readiness could not be loaded or no agent can launch, so an unranked fallback
 * never lands in the config. The project is read again right before the save;
 * the PUT replaces the whole config and carries no precondition, so a settings
 * save landing between that read and the write is lost.
 */
export async function fillMissingProjectRoleAgents(queryClient: QueryClient, projectId: string, hostId?: string): Promise<void> {
	const client = hostId ? clientForHost(hostId) : apiClient;
	const readProject = async () => {
		const current = await client.GET("/api/v1/projects/{id}", { params: { path: { id: projectId } } });
		if (current.error) throw apiErrorException(current.error);
		if (current.data?.status !== "ok" || !current.data.project) return undefined;
		const project = current.data.project;
		// A project whose folder can't be resolved has no config to fill; the start
		// that follows reports that problem itself.
		if ("resolveError" in project) return undefined;
		const config: ProjectConfig = structuredClone(project.config ?? {});
		return config.worker?.agent && config.orchestrator?.agent ? undefined : { name: project.name, config };
	};
	if (!await readProject()) return;
	const defaults = await loadRankedProjectAgentDefaults(queryClient, hostId);
	if (!defaults) return;
	const project = await readProject();
	if (!project) return;
	const { config } = project;
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
