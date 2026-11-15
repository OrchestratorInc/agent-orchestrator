import { apiErrorException } from "../lib/api-error-exception";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import type { components } from "../../api/schema";
import { artifactChangeError } from "../lib/artifacts";
import type { CloudCpClient } from "../lib/cloud-cp";
import { clientForSessionHost } from "../lib/host-clients";
import { sessionArtifactFileQueryOptions } from "./useSessionWorkspaceFiles";
import { cloudSessionsQueryKey, workspaceQueryKeyForHost } from "./useWorkspaceQuery";

export type ArtifactVersionSummary = components["schemas"]["ArtifactVersionSummary"];
export type ArtifactVersion = components["schemas"]["ArtifactVersionResponse"];

const versionsKey = (sessionId: string, path: string, hostId?: string) =>
	["session-artifact-versions", hostId ?? "", sessionId, path] as const;

export interface CloudArtifactVersions {
	client: CloudCpClient;
	orgId: string;
}

export function useArtifactVersions({ cloud, enabled, hostId, path, sessionId }: { cloud?: CloudArtifactVersions; enabled: boolean; hostId?: string; path: string; sessionId: string }) {
	return useQuery({
		enabled,
		queryKey: versionsKey(sessionId, path, hostId),
		retry: 1,
		queryFn: async (): Promise<ArtifactVersionSummary[]> => {
			if (cloud) return (await cloud.client.listSessionArtifactVersions(cloud.orgId, sessionId, path)).versions;
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/artifact-versions", {
				params: { path: { sessionId }, query: { path } },
			});
			if (error) throw apiErrorException(error, "Unable to load version history");
			return data?.versions ?? [];
		},
	});
}

export function useHostServesMediaVersions({ enabled, hostId, path, sessionId }: { enabled: boolean; hostId?: string; path: string; sessionId: string }): boolean {
	const { data } = useQuery({
		enabled,
		queryKey: ["artifact-media-versions", hostId ?? ""],
		retry: 1,
		staleTime: 5 * 60_000,
		queryFn: async (): Promise<boolean> => {
			const { data: list, error } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/artifact-versions", {
				params: { path: { sessionId }, query: { path } },
			});
			if (error) throw apiErrorException(error, "Unable to load version history");
			return list?.mediaVersions === true;
		},
	});
	return data === true;
}

export function useSyncArtifactVersions({ cloud, hostId, path, sessionId }: { cloud?: CloudArtifactVersions; hostId?: string; path: string; sessionId: string }) {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: async (): Promise<ArtifactVersionSummary[]> => {
			if (cloud) return (await cloud.client.listSessionArtifactVersions(cloud.orgId, sessionId, path)).versions;
			const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/artifact-versions/sync", {
				params: { path: { sessionId } },
				body: { path },
			});
			if (error) throw apiErrorException(error, "Unable to load version history");
			return data?.versions ?? [];
		},
		onSuccess: (versions) => queryClient.setQueryData(versionsKey(sessionId, path, hostId), versions),
	});
}

export function useArtifactVersion({ cloud, hostId, path, sessionId, version }: { cloud?: CloudArtifactVersions; hostId?: string; path: string; sessionId: string; version: number | null }) {
	return useQuery({
		enabled: version !== null,
		queryKey: [...versionsKey(sessionId, path, hostId), version] as const,
		queryFn: async (): Promise<ArtifactVersion> => {
			if (cloud) return cloud.client.getSessionArtifactVersion(cloud.orgId, sessionId, path, version!);
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/artifact-versions/{version}", {
				params: { path: { sessionId, version: version! }, query: { path } },
			});
			if (error) throw apiErrorException(error, "Unable to load this version");
			if (!data) throw new Error("Unable to load this version");
			return data;
		},
		staleTime: Infinity,
	});
}

export function useRestoreArtifactVersion({ cloud, hostId, path, rawUrl, sessionId }: { cloud?: CloudArtifactVersions; hostId?: string; path: string; rawUrl?: string; sessionId: string }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: async ({ version }: { version: number }) => {
			if (cloud) {
				return cloud.client.restoreSessionArtifactVersion(cloud.orgId, sessionId, path, version)
					.catch((error: unknown) => { throw artifactChangeError(error, t("artifacts.viewOnly")); });
			}
			const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/artifact-versions/restore", {
				params: { path: { sessionId } },
				body: { path, version },
			});
			if (error) throw apiErrorException(error, "Unable to restore this version");
			if (!data) throw new Error("Unable to restore this version");
			return data;
		},
		onSuccess: async () => {
			await Promise.all([
				queryClient.invalidateQueries({ queryKey: sessionArtifactFileQueryOptions(sessionId, path, rawUrl).queryKey }),
				queryClient.invalidateQueries({ queryKey: versionsKey(sessionId, path, hostId) }),
				queryClient.invalidateQueries({ queryKey: cloud ? cloudSessionsQueryKey : workspaceQueryKeyForHost(hostId) }),
			]);
		},
	});
}
