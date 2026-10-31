import { type QueryClient, useMutation, useQueryClient } from "@tanstack/react-query";
import type { WorkspaceSession } from "../types/workspace";
import { cloudSessionsQueryKey, workspaceQueryKeyForHost } from "./useWorkspaceQuery";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import type { CloudCpSession } from "../lib/cloud-cp";
import { createRendererCloudCpClient } from "../lib/cloud-cp/renderer-client";
import { clientForSessionHost } from "../lib/host-clients";
import { settingsQueryKey, type Settings } from "./useSettings";

export const pinSessionMutationKey = ["pin-session"] as const;
export const unpinSessionMutationKey = ["unpin-session"] as const;

type CloudSessionsSnapshot = [readonly unknown[], CloudCpSession[] | undefined][];

// A Cloud pin is the caller's own and lives on the control plane. The board
// and sidebar read Cloud rows from cloudSessionsQueryKey, so the row moves at
// once and moves back if the control plane refuses.
async function setCloudPinned(queryClient: QueryClient, session: WorkspaceSession, pinned: boolean): Promise<void> {
	const baseUrl = queryClient.getQueryData<Settings>(settingsQueryKey)?.cloudControlPlaneUrl ?? "";
	if (!session.cloud || baseUrl === "") throw new Error("The cloud control plane is not configured.");
	await createRendererCloudCpClient(baseUrl).setSessionPinned(session.cloud.orgId, session.id, pinned);
}

async function markCloudPinned(queryClient: QueryClient, session: WorkspaceSession, pinned: boolean): Promise<CloudSessionsSnapshot | undefined> {
	if (!session.cloud) return undefined;
	await queryClient.cancelQueries({ queryKey: cloudSessionsQueryKey });
	const snapshot = queryClient.getQueriesData<CloudCpSession[]>({ queryKey: cloudSessionsQueryKey });
	const pinnedAt = new Date().toISOString();
	queryClient.setQueriesData<CloudCpSession[]>({ queryKey: cloudSessionsQueryKey }, (current) =>
		current?.map((candidate) => candidate.id === session.id
			? { ...candidate, pinned, pinnedAt: pinned ? candidate.pinnedAt ?? pinnedAt : undefined }
			: candidate));
	return snapshot;
}

function restoreCloudSessions(queryClient: QueryClient, snapshot: CloudSessionsSnapshot | undefined) {
	for (const [key, data] of snapshot ?? []) queryClient.setQueryData(key, data);
}

function refreshAfterPin(queryClient: QueryClient, session: WorkspaceSession) {
	return queryClient.invalidateQueries({ queryKey: session.cloud ? cloudSessionsQueryKey : workspaceQueryKeyForHost(session.hostId) });
}

export function usePinSession() {
	const queryClient = useQueryClient();
	return useMutation({
		mutationKey: pinSessionMutationKey,
		mutationFn: async (session: WorkspaceSession) => {
			if (session.cloud) return setCloudPinned(queryClient, session, true);
			const { error, response } = await (session.hostId ? clientForSessionHost(session.hostId) : apiClient).POST("/api/v1/sessions/{sessionId}/pin", {
				params: { path: { sessionId: session.id } },
			});
			if (error) {
				const fallback = response ? `Failed to pin session (${response.status})` : "Failed to pin session";
				throw new Error(apiErrorMessage(error, fallback));
			}
		},
		onMutate: (session) => markCloudPinned(queryClient, session, true),
		onSuccess: async (_data, session) => {
			await refreshAfterPin(queryClient, session);
		},
		onError: (error, _session, snapshot) => {
			restoreCloudSessions(queryClient, snapshot);
			console.error("Failed to pin session:", error);
		},
	});
}

export function useUnpinSession() {
	const queryClient = useQueryClient();
	return useMutation({
		mutationKey: unpinSessionMutationKey,
		mutationFn: async (session: WorkspaceSession) => {
			if (session.cloud) return setCloudPinned(queryClient, session, false);
			const { error, response } = await (session.hostId ? clientForSessionHost(session.hostId) : apiClient).DELETE("/api/v1/sessions/{sessionId}/pin", {
				params: { path: { sessionId: session.id } },
			});
			if (error) {
				const fallback = response ? `Failed to unpin session (${response.status})` : "Failed to unpin session";
				throw new Error(apiErrorMessage(error, fallback));
			}
		},
		onMutate: (session) => markCloudPinned(queryClient, session, false),
		onSuccess: async (_data, session) => {
			await refreshAfterPin(queryClient, session);
		},
		onError: (error, _session, snapshot) => {
			restoreCloudSessions(queryClient, snapshot);
			console.error("Failed to unpin session:", error);
		},
	});
}
