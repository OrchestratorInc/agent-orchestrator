import type { QueryClient } from "@tanstack/react-query";
import { apiClient, apiErrorMessage } from "./api-client";
import { clientForSessionHost } from "./host-clients";
import type { CloudCpSession } from "./cloud-cp";
import { createRendererCloudCpClient } from "./cloud-cp/renderer-client";
import { settingsQueryKey, type Settings } from "../hooks/useSettings";
import { cloudSessionsQueryKey, refreshCloudSessionLifecycle } from "../hooks/useWorkspaceQuery";
import type { WorkspaceSession } from "../types/workspace";

/** Update a session's display name via the daemon (PATCH /sessions/{id}). The
 *  daemon enforces the same 100-character limit as the spawn `--name` flag. */
export async function renameSession(sessionId: string, displayName: string, hostId?: string): Promise<void> {
	const { error, response } = await (hostId ? clientForSessionHost(hostId) : apiClient).PATCH("/api/v1/sessions/{sessionId}", {
		params: { path: { sessionId } },
		body: { displayName },
	});

	if (error) {
		throw new Error(apiErrorMessage(error, `Failed to rename session${response ? ` (${response.status})` : ""}`));
	}
}

/**
 * Renames a Cloud session on the control plane, so everyone in it sees the new
 * title. It shows in the cached session lists at once and then refreshes them.
 */
export async function renameCloudSession(
	queryClient: QueryClient,
	session: Pick<WorkspaceSession, "id"> & { cloud: { orgId: string } },
	displayName: string,
): Promise<void> {
	const baseUrl = queryClient.getQueryData<Settings>(settingsQueryKey)?.cloudControlPlaneUrl ?? "";
	if (baseUrl === "") throw new Error("The cloud control plane is not configured.");
	const { session: renamed } = await createRendererCloudCpClient(baseUrl).renameSession(session.cloud.orgId, session.id, displayName);
	queryClient.setQueriesData<CloudCpSession[]>({ queryKey: cloudSessionsQueryKey }, (current) =>
		current?.map((candidate) => candidate.id === session.id ? { ...candidate, displayName: renamed.displayName } : candidate));
	await refreshCloudSessionLifecycle(queryClient);
}
