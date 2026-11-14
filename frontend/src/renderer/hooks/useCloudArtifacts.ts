import { useQuery } from "@tanstack/react-query";
import { useCallback, useState } from "react";
import type { WorkspaceSession } from "../types/workspace";
import { useCloudCp } from "./useCloudCp";

const ACCESS_REFRESH_MARGIN_MS = 2 * 60_000;
const ACCESS_MIN_REFRESH_MS = 30_000;

export const cloudArtifactAccessQueryKey = (baseUrl: string, orgId: string, sessionId: string) =>
	["cloud-artifact-access", baseUrl, orgId, sessionId] as const;

export const cloudArtifactFilesQueryKey = (baseUrl: string, orgId: string, sessionId: string, version: string) =>
	[...cloudArtifactSessionFilesQueryKey(baseUrl, orgId, sessionId), version] as const;

export const cloudArtifactSessionFilesQueryKey = (baseUrl: string, orgId: string, sessionId: string) =>
	["cloud-artifact-files", baseUrl, orgId, sessionId] as const;

type CloudArtifactAccess = { root: string; expiresAt: number };

function accessRefreshIn(access: CloudArtifactAccess | undefined): number {
	if (!access) return 0;
	return Math.max(ACCESS_MIN_REFRESH_MS, access.expiresAt - Date.now() - ACCESS_REFRESH_MARGIN_MS);
}

export function useCloudArtifactAccess(session: Pick<WorkspaceSession, "id" | "cloud">, enabled = true) {
	const { client, ready, baseUrl } = useCloudCp(enabled);
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

export function cloudArtifactFileUrl(root: string, path: string, version?: string): string {
	const url = root + path.split("/").map(encodeURIComponent).join("/");
	return version ? `${url}?v=${encodeURIComponent(version)}` : url;
}

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
	const repin = useCallback(() => setPinned((previous) => (previous?.url ? { ...previous, refused: previous.url } : previous)), []);
	const refresh = useCallback(() => setPinned(undefined), []);
	return { url: current.url, root: current.root, repin, refresh };
}
