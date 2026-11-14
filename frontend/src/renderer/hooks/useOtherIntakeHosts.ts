import { useQueries, useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { apiClient } from "../lib/api-client";
import { clientForHost, labelForHost } from "../lib/host-clients";
import { LOCAL_HOST, type HostId } from "../lib/hosts";
import { projectsWithRepo } from "../lib/repo-hosts";
import { useRemoteWorkspaces, workspaceQueryOptions } from "./useWorkspaceQuery";

/**
 * The other computers whose project for this repository has issue intake on.
 * Intake only checks its own computer's sessions, so each of them would start
 * a worker for the same assigned issue.
 */
export function useOtherIntakeHosts(repoKey: string | undefined, self: { hostId?: HostId; projectId: string }): string[] {
	const { t } = useTranslation();
	const local = useQuery({ ...workspaceQueryOptions, enabled: Boolean(repoKey) }).data ?? [];
	const remote = useRemoteWorkspaces({ enabled: Boolean(repoKey) }).data ?? [];
	const selfHost = self.hostId ?? LOCAL_HOST;
	const others = projectsWithRepo(local, remote, repoKey).filter((project) => !(project.hostId === selfHost && project.projectId === self.projectId));
	const intake = useQueries({
		queries: others.map((project) => ({
			queryKey: ["project-intake", project.hostId, project.projectId],
			staleTime: 60_000,
			queryFn: async () => {
				const client = project.hostId === LOCAL_HOST ? apiClient : clientForHost(project.hostId);
				const { data } = await client.GET("/api/v1/projects/{id}", { params: { path: { id: project.projectId } } });
				const found = data?.project;
				return Boolean(found && "config" in found && found.config?.trackerIntake?.enabled);
			},
		})),
	});
	const names = others
		.filter((_, index) => intake[index]?.data)
		.map((project) => (project.hostId === LOCAL_HOST ? t("remote.thisComputer") : labelForHost(project.hostId) ?? project.hostId));
	return [...new Set(names)];
}
