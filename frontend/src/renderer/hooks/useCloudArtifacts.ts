import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import type { WorkspaceSession } from "../types/workspace";
import { useCloudCp } from "./useCloudCp";

/** How long before an access prefix expires it is replaced. */
const ACCESS_REFRESH_MARGIN_MS = 2 * 60_000;
/** The soonest an access prefix is refreshed again, whatever its expiry says. */
const ACCESS_MIN_REFRESH_MS = 30_000;

export const cloudArtifactAccessQueryKey = (baseUrl: string, orgId: string, sessionId: string) =>
	["cloud-artifact-access", baseUrl, orgId, sessionId] as const;

/**
 * The query key of one cloud session's artifact list at one version (its
 * artifact count and newest commit time), so a cached list is reused until a
 * commit changes the files.
 */
export const cloudArtifactFilesQueryKey = (baseUrl: string, orgId: string, sessionId: string, version: string) =>
	["cloud-artifact-files", baseUrl, orgId, sessionId, version] as const;

type CloudArtifactAccess = { root: string; expiresAt: number };

/** Milliseconds until an access prefix should be replaced. */
function accessRefreshIn(access: CloudArtifactAccess | undefined): number {
	if (!access) return 0;
	return Math.max(ACCESS_MIN_REFRESH_MS, access.expiresAt - Date.now() - ACCESS_REFRESH_MARGIN_MS);
}

/**
 * The absolute URL, ending in "/", that serves a cloud session's artifact
 * files. A file's URL is this root plus its escaped path; relative links
 * inside an HTML or Markdown file resolve against it. It is replaced shortly
 * before it expires, and on window focus once it is about to.
 */
export function useCloudArtifactAccess(session: WorkspaceSession, enabled = true) {
	const { client, ready, baseUrl } = useCloudCp();
	const orgId = session.cloud?.orgId ?? "";
	return useQuery<CloudArtifactAccess, Error, string, ReturnType<typeof cloudArtifactAccessQueryKey>>({
		queryKey: cloudArtifactAccessQueryKey(baseUrl, orgId, session.id),
		enabled: enabled && ready && orgId !== "",
		staleTime: (query) => accessRefreshIn(query.state.data),
		refetchInterval: (query) => accessRefreshIn(query.state.data),
		refetchOnWindowFocus: true,
		retry: 1,
		queryFn: async ({ signal }): Promise<CloudArtifactAccess> => {
			const access = await client.createSessionArtifactAccess(orgId, session.id, { signal });
			return { root: `${baseUrl.replace(/\/+$/, "")}${access.filesPrefix}`, expiresAt: Date.parse(access.expiresAt) };
		},
		select: (access) => access.root,
	});
}

/** One artifact file's URL under an access root from useCloudArtifactAccess.
 * `version` (the file's updatedAt) becomes a query parameter, so the URL
 * changes when the file does and an open view reloads it. */
export function cloudArtifactFileUrl(root: string, path: string, version?: string): string {
	const url = root + path.split("/").map(encodeURIComponent).join("/");
	return version ? `${url}?v=${encodeURIComponent(version)}` : url;
}

/**
 * The URL an open cloud file is shown from, and the access root it was taken
 * from (for its relative links). It is taken when the file opens or changes,
 * and kept while the root is refreshed, so a refresh does not reload the open
 * file. After a read with the kept URL was refused, repin keeps it until a
 * root gives a different URL, then takes that one; refresh takes the current
 * root now, for a view that reloads anyway.
 */
export function usePinnedArtifactUrl(root: string | undefined, path: string | undefined, version: string | undefined) {
	const [pinned, setPinned] = useState<{ key: string; url?: string; root?: string; refused?: string }>();
	const key = path === undefined ? "" : `${path}\n${version ?? ""}`;
	let current = pinned?.key === key ? pinned : { key };
	if ((current.url === undefined || current.refused !== undefined) && root !== undefined && path !== undefined) {
		const url = cloudArtifactFileUrl(root, path, version);
		if (url !== current.refused) {
			current = { key, url, root };
			setPinned(current);
		}
	}
	return {
		url: current.url,
		root: current.root,
		repin: () => setPinned((previous) => (previous?.url ? { ...previous, refused: previous.url } : previous)),
		refresh: () => setPinned(undefined),
	};
}
