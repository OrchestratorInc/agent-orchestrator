import { useQueries } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { repositoryOwnerAvatar } from "../lib/repository-avatar";
import { useUiStore } from "../stores/ui-store";
import { CLOUD_PROJECT_KIND, STANDALONE_PROJECT_KIND, type WorkspaceSummary } from "../types/workspace";

type CachedAvatar = { url: string; src: string };

const cacheKey = "ao.projectAvatarCache";

function readCache(): Record<string, CachedAvatar> {
	try {
		return JSON.parse(window.localStorage.getItem(cacheKey) ?? "{}") as Record<string, CachedAvatar>;
	} catch {
		return {};
	}
}

// The image as a data URL, so the next launch paints it with no network
// request. Hosts without CORS fall back to the plain URL.
async function toImageSource(url: string): Promise<string> {
	try {
		const response = await fetch(url);
		if (!response.ok) return url;
		const blob = await response.blob();
		return await new Promise<string>((resolve) => {
			const reader = new FileReader();
			reader.onload = () => resolve(typeof reader.result === "string" ? reader.result : url);
			reader.onerror = () => resolve(url);
			reader.readAsDataURL(blob);
		});
	} catch {
		return url;
	}
}

// Fetched once per launch, so an owner's new avatar replaces the cached one.
const refreshedUrls = new Set<string>();

// The image source for each local project's repository owner avatar, keyed by
// project id. The last known images come from localStorage on first render, so
// the sidebar paints them instantly; project details then confirm the owner
// and refresh the cache in the background. Empty when the setting is off.
export function useProjectAvatars(workspaces: WorkspaceSummary[]): Map<string, string> {
	const enabled = useUiStore((state) => state.projectAvatars);
	const [cache, setCache] = useState(readCache);
	const projects = useMemo(
		() => workspaces.filter((workspace) => !workspace.hostId && workspace.kind !== CLOUD_PROJECT_KIND && workspace.kind !== STANDALONE_PROJECT_KIND),
		[workspaces],
	);
	const details = useQueries({
		queries: projects.map((project) => ({
			queryKey: ["project", project.id],
			enabled,
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
	const urls = useMemo(() => {
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
	const urlKey = [...urls].map(([id, url]) => `${id}=${url}`).join("\n");

	useEffect(() => {
		if (!enabled) return;
		for (const [projectId, url] of urls) {
			if (refreshedUrls.has(url)) continue;
			refreshedUrls.add(url);
			void toImageSource(url).then((src) => {
				setCache((previous) => {
					if (previous[projectId]?.url === url && previous[projectId]?.src === src) return previous;
					const next = { ...previous, [projectId]: { url, src } };
					try {
						window.localStorage.setItem(cacheKey, JSON.stringify(next));
					} catch {
						// A full localStorage only costs the instant first paint.
					}
					return next;
				});
			});
		}
		// urlKey carries the content of urls.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [enabled, urlKey]);

	return useMemo(() => {
		const map = new Map<string, string>();
		if (!enabled) return map;
		for (const project of projects) {
			const url = urls.get(project.id);
			const cached = cache[project.id];
			// Before details load, trust the cache; after, only a cache for the same owner.
			if (cached && (!url || cached.url === url)) map.set(project.id, cached.src);
			else if (url) map.set(project.id, url);
		}
		return map;
	}, [cache, enabled, projects, urls]);
}
