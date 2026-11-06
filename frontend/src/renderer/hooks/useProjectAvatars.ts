import { useQueries } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { apiClient } from "../lib/api-client";
import { apiErrorException } from "../lib/api-error-exception";
import { repositoryOwnerAvatar } from "../lib/repository-avatar";
import { useUiStore } from "../stores/ui-store";
import { isNewSidebarDesign } from "../lib/new-sidebar-design";
import { CLOUD_PROJECT_KIND, STANDALONE_PROJECT_KIND, type WorkspaceSummary } from "../types/workspace";

// Avatar URL -> the image as a data URL, so a launch paints known avatars with
// no network request.
type AvatarCache = Record<string, string>;

const cacheKey = "ao.projectAvatarCache";
// Project id -> its last avatar URL, so the first paint does not wait for
// project details.
const urlsKey = "ao.projectAvatarUrls";

function readStrings(key: string): Record<string, string> {
	try {
		const parsed: unknown = JSON.parse(window.localStorage.getItem(key) ?? "{}");
		if (!parsed || typeof parsed !== "object") return {};
		return Object.fromEntries(Object.entries(parsed).filter((entry): entry is [string, string] => typeof entry[1] === "string"));
	} catch {
		return {};
	}
}

function writeStrings(key: string, value: Record<string, string>) {
	try {
		window.localStorage.setItem(key, JSON.stringify(value));
	} catch {
		// A full localStorage only costs the instant first paint.
	}
}

function readCache(): AvatarCache {
	return Object.fromEntries(Object.entries(readStrings(cacheKey)).filter(([, src]) => src.startsWith("data:image/")));
}

// Draws the avatar through a CORS image and a canvas. Loading it as an image
// keeps it under the renderer's img-src policy (fetch would need connect-src),
// and a redirect to a sign-in page fails to decode instead of being cached.
function loadAsDataUrl(url: string): Promise<string | null> {
	return new Promise((resolve) => {
		const image = new Image();
		image.crossOrigin = "anonymous";
		image.onload = () => {
			try {
				const canvas = document.createElement("canvas");
				canvas.width = 64;
				canvas.height = 64;
				const context = canvas.getContext("2d");
				if (!context) return resolve(null);
				context.drawImage(image, 0, 0, 64, 64);
				resolve(canvas.toDataURL("image/png"));
			} catch {
				resolve(null);
			}
		};
		image.onerror = () => resolve(null);
		image.src = url;
	});
}

// Refreshed once per launch, so an owner's new avatar replaces the cached one.
const refreshedUrls = new Set<string>();

// The image source for each local project's repository owner avatar, keyed by
// project id. Cached images come from localStorage on first render, so the
// sidebar paints them instantly; project details then confirm the owner and
// refresh the cache in the background. A failed refresh keeps the cached
// image. Empty when the setting is off.
export function useProjectAvatars(workspaces: WorkspaceSummary[]): Map<string, string> {
	const enabled = useUiStore((state) => state.projectAvatars && isNewSidebarDesign(state));
	const [cache, setCache] = useState(readCache);
	const [knownUrls] = useState(() => readStrings(urlsKey));
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
				if (error) throw apiErrorException(error);
				if (data?.status !== "ok") return undefined;
				return data.project;
			},
		})),
	});
	const urls = useMemo(() => {
		const map = new Map<string, string>();
		projects.forEach((project, index) => {
			const query = details[index];
			if (!query?.isSuccess) {
				const known = knownUrls[project.id];
				if (known) map.set(project.id, known);
				return;
			}
			const detail = query.data;
			const repo = detail && "repo" in detail && typeof detail.repo === "string" && detail.repo
				? detail.repo
				: project.workspaceRepos?.[0]?.repo;
			const avatar = repo ? repositoryOwnerAvatar(repo) : null;
			if (avatar) map.set(project.id, avatar.url);
		});
		return map;
	}, [details, knownUrls, projects]);
	const urlKey = [...new Set(urls.values())].sort().join("\n");
	const settled = details.every((query) => !query.isPending);
	const urlsJson = JSON.stringify(Object.fromEntries(urls));
	useEffect(() => {
		if (!enabled || !settled) return;
		try {
			window.localStorage.setItem(urlsKey, urlsJson);
		} catch {
			// A full localStorage only costs the instant first paint.
		}
	}, [enabled, settled, urlsJson]);

	useEffect(() => {
		if (!enabled) return;
		const current = new Set(urlKey ? urlKey.split("\n") : []);
		for (const url of current) {
			if (refreshedUrls.has(url)) continue;
			refreshedUrls.add(url);
			void loadAsDataUrl(url).then((src) => {
				if (!src) return;
				setCache((previous) => {
					if (previous[url] === src) return previous;
					// Keep only avatars still in use, so removed projects drop out.
					const next: AvatarCache = { [url]: src };
					for (const [key, value] of Object.entries(previous)) if (current.has(key) && key !== url) next[key] = value;
					writeStrings(cacheKey, next);
					return next;
				});
			});
		}
	}, [enabled, urlKey]);

	return useMemo(() => {
		const map = new Map<string, string>();
		if (!enabled) return map;
		for (const [projectId, url] of urls) map.set(projectId, cache[url] ?? url);
		return map;
	}, [cache, enabled, urls]);
}
