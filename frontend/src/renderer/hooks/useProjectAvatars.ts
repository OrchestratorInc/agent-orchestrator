import { useQueries } from "@tanstack/react-query";
import { useEffect, useMemo } from "react";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { repositoryOwnerAvatar } from "../lib/repository-avatar";
import { CLOUD_PROJECT_KIND, STANDALONE_PROJECT_KIND, type WorkspaceSummary } from "../types/workspace";

// The owner avatar of each local project's repository, keyed by project id.
// Fetched and preloaded as soon as the sidebar mounts so the icons are ready
// before a project row is shown. Shares the ["project", id] cache entry with
// the New Task composer.
export function useProjectAvatars(workspaces: WorkspaceSummary[]): Map<string, string> {
	const projects = useMemo(
		() => workspaces.filter((workspace) => !workspace.hostId && workspace.kind !== CLOUD_PROJECT_KIND && workspace.kind !== STANDALONE_PROJECT_KIND),
		[workspaces],
	);
	const details = useQueries({
		queries: projects.map((project) => ({
			queryKey: ["project", project.id],
			staleTime: 30_000,
			queryFn: async () => {
				const { data, error } = await apiClient.GET("/api/v1/projects/{id}", {
					params: { path: { id: project.id } },
				});
				if (error) throw new Error(apiErrorMessage(error));
				if (data?.status !== "ok") return undefined;
				return data.project;
			},
		})),
	});
	const avatars = useMemo(() => {
		const map = new Map<string, string>();
		projects.forEach((project, index) => {
			const detail = details[index]?.data;
			const repo = detail && "repo" in detail && typeof detail.repo === "string" && detail.repo
				? detail.repo
				: project.workspaceRepos?.[0]?.repo;
			const avatar = repo ? repositoryOwnerAvatar(repo) : null;
			if (avatar) map.set(project.id, avatar.url);
		});
		return map;
	}, [details, projects]);
	const urls = [...avatars.values()].join("\n");
	useEffect(() => {
		for (const url of urls.split("\n")) if (url) new Image().src = url;
	}, [urls]);
	return avatars;
}
