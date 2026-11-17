import { useQuery } from "@tanstack/react-query";
import { clientForSessionHost } from "../lib/host-clients";
import { trackerIssueLink, STANDALONE_WORKSPACE_ID, type WorkspaceSession } from "../types/workspace";

export function useSessionIssueLink(session: WorkspaceSession, hostId = session.hostId) {
	const project = useQuery({
		queryKey: hostId ? ["project", hostId, session.workspaceId] : ["project", session.workspaceId],
		enabled: /^(?:[\w.-]+(?:\/[\w.-]+)+#|#?)[1-9]\d*$/.test(session.issueId?.trim() ?? "") && !session.cloud && session.workspaceId !== STANDALONE_WORKSPACE_ID,
		staleTime: 30_000,
		queryFn: async () => {
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/projects/{id}", {
				params: { path: { id: session.workspaceId } },
			});
			if (error) throw error;
			return data?.status === "ok" && "repo" in data.project ? data.project : undefined;
		},
	});
	const intake = project.data?.config?.trackerIntake;
	return trackerIssueLink(session.issueId, project.data?.repo, intake?.repo ? undefined : intake?.provider);
}
