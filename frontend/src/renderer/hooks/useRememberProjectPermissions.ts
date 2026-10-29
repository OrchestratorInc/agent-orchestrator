import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import { apiErrorMessage } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";
import type { ApprovalMode } from "../types/conversation";
import { STANDALONE_WORKSPACE_ID, type SessionKind } from "../types/workspace";
import { remoteWorkspaceQueryKey, workspaceQueryKey } from "./useWorkspaceQuery";

type ProjectPermissionsConfig = {
	config?: {
		agentConfig?: { permissions?: string };
		worker?: { agentConfig?: { permissions?: string } };
		orchestrator?: { agentConfig?: { permissions?: string } };
	};
};

/**
 * The project's remembered permission mode, and saving or forgetting it. Only an
 * explicit toggle writes it; selecting a session mode is separate. `null` forgets.
 * A Scratchpad session has no project to read.
 */
export function useRememberProjectPermissions(projectId: string, sourceHarness?: string, hostId?: string, kind?: SessionKind) {
	const queryClient = useQueryClient();
	const hasProject = Boolean(projectId) && projectId !== STANDALONE_WORKSPACE_ID;
	const projectKey = hostId ? ["project", projectId, hostId] : ["project", projectId];
	const project = useQuery({
		queryKey: projectKey,
		enabled: hasProject,
		queryFn: async () => {
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/projects/{id}", {
				params: { path: { id: projectId } },
			});
			if (error) throw new Error(apiErrorMessage(error));
			if (data?.status !== "ok") throw new Error("Project config unavailable");
			return data.project as ProjectPermissionsConfig;
		},
	});
	const mutation = useMutation({
		mutationFn: async ({ targetProjectId, permissions, sourceHarness }: { targetProjectId: string; permissions: ApprovalMode | null; sourceHarness?: string }) => {
			const client = clientForSessionHost(hostId);
			const { error } = permissions === null
				? await client.DELETE("/api/v1/projects/{id}/permissions", { params: { path: { id: targetProjectId } } })
				: await client.PATCH("/api/v1/projects/{id}/permissions", {
					params: { path: { id: targetProjectId } },
					body: { permissions, ...(sourceHarness ? { sourceHarness } : {}) },
				});
			if (error) throw error;
		},
		onSuccess: (_data, { targetProjectId }) => {
			void Promise.all([
				queryClient.invalidateQueries({ queryKey: hostId ? ["project", targetProjectId, hostId] : ["project", targetProjectId] }),
				queryClient.invalidateQueries({ queryKey: hostId ? remoteWorkspaceQueryKey(hostId) : workspaceQueryKey }),
			]).catch(() => {});
		},
	});
	const remember = useCallback((permissions: ApprovalMode | null) =>
		mutation.mutateAsync({ targetProjectId: projectId, permissions, sourceHarness }), [mutation.mutateAsync, projectId, sourceHarness]);
	const belongsToProject = mutation.variables?.targetProjectId === projectId;
	const config = project.data?.config;
	const role = kind === "orchestrator" ? config?.orchestrator : config?.worker;
	const stored = (role?.agentConfig?.permissions || config?.agentConfig?.permissions || undefined) as ApprovalMode | undefined;
	// The last write shows until the refetched project catches up, then the project
	// wins again, so a change made in project settings shows here too. The daemon
	// stores Codex's default as Full access, so the saved mode reads the same for every agent.
	const written = mutation.variables;
	const savedMode = belongsToProject && mutation.isSuccess && written && project.dataUpdatedAt < mutation.submittedAt
		? written.sourceHarness === "codex" && written.permissions === "default" ? "bypass-permissions" : written.permissions ?? undefined
		: stored;
	return {
		remember,
		pending: belongsToProject && mutation.isPending,
		savedMode,
		error: belongsToProject && mutation.error
			? apiErrorMessage(mutation.error, "Could not remember permissions for this project.")
			: undefined,
	};
}
