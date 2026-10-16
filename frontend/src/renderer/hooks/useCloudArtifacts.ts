import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { subscribeSessionEventsBridged } from "../lib/cloud-cp/stream-bridge";
import type { WorkspaceSession } from "../types/workspace";
import { useCloudCp } from "./useCloudCp";
import { cloudSessionsQueryKey } from "./useWorkspaceQuery";

/** The control plane signs an access prefix for 30 minutes; refresh well before. */
const ACCESS_REFRESH_MS = 20 * 60_000;

export const cloudArtifactAccessQueryKey = (baseUrl: string, orgId: string, sessionId: string) =>
	["cloud-artifact-access", baseUrl, orgId, sessionId] as const;

/**
 * The absolute URL, ending in "/", that serves a cloud session's artifact
 * files. A file's URL is this root plus its escaped path; relative links
 * inside an HTML or Markdown file resolve against it.
 */
export function useCloudArtifactAccess(session: WorkspaceSession, enabled = true) {
	const { client, ready, baseUrl } = useCloudCp();
	const orgId = session.cloud?.orgId ?? "";
	return useQuery({
		queryKey: cloudArtifactAccessQueryKey(baseUrl, orgId, session.id),
		enabled: enabled && ready && orgId !== "",
		staleTime: ACCESS_REFRESH_MS,
		refetchInterval: ACCESS_REFRESH_MS,
		retry: 1,
		queryFn: async ({ signal }) => {
			const access = await client.createSessionArtifactAccess(orgId, session.id, { signal });
			return `${baseUrl.replace(/\/+$/, "")}${access.filesPrefix}`;
		},
	});
}

/**
 * One artifact file's URL under an access root from useCloudArtifactAccess.
 * `version` (the file's updatedAt) becomes a query parameter, so the URL
 * changes when the file does and an open view reloads it.
 */
export function cloudArtifactFileUrl(root: string, path: string, version?: string): string {
	const url = root + path.split("/").map(encodeURIComponent).join("/");
	return version ? `${url}?v=${encodeURIComponent(version)}` : url;
}

/** Refreshes the cloud session list when the session's artifacts change. */
export function useCloudArtifactChanges(session: WorkspaceSession, enabled = true) {
	const queryClient = useQueryClient();
	const { baseUrl } = useCloudCp();
	const orgId = session.cloud?.orgId;
	const sessionId = session.id;
	useEffect(() => {
		if (!enabled || !baseUrl || !orgId) return;
		const controller = new AbortController();
		const subscribedAt = Date.now();
		void subscribeSessionEventsBridged({
			baseUrl,
			orgId,
			sessionId,
			after: 0,
			signal: controller.signal,
			onEvent: (event) => {
				if (event.type !== "artifact.changed" || Date.parse(event.createdAt) < subscribedAt - 5_000) return;
				void queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey });
			},
		});
		return () => controller.abort();
	}, [baseUrl, enabled, orgId, queryClient, sessionId]);
}
