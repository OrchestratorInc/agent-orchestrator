import { useNavigate } from "@tanstack/react-router";
import { type QueryClient, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { apiClient, apiErrorCode } from "../lib/api-client";
import { apiErrorException } from "../lib/api-error-exception";
import type { CloudCpClient, CloudCpProject } from "../lib/cloud-cp";
import { clientForHost } from "../lib/host-clients";
import { LOCAL_HOST, refKey } from "../lib/hosts";
import { addRendererExceptionStep, captureRendererEvent, captureRendererException } from "../lib/telemetry";
import { useSidebarOrderStore } from "../stores/sidebar-order-store";
import { useUiStore } from "../stores/ui-store";
import { CLOUD_PROJECT_KIND, type WorkspaceSummary } from "../types/workspace";
import {
	applyOptimisticProjectRemovals,
	clearOptimisticProjectRemoval,
	isOptimisticallyRemovedProject,
	trackOptimisticProjectRemoval,
} from "./optimistic-project-removals";
import { useLatestCallback } from "./useStable";
import {
	cloudProjectsQueryKey,
	cloudSessionsQueryKey,
	remoteProjectsQueryKey,
	remoteSessionsQueryKey,
	remoteWorkspaceQueryKey,
	sharedCloudWorkspacesQueryKey,
	workspaceQueryKey,
} from "./useWorkspaceQuery";

export const PROJECT_REMOVE_ERROR_TOAST_MS = 12_000;

type RemoveTarget = {
	workspace: WorkspaceSummary;
	cloud: boolean;
	cacheKey: readonly unknown[];
};

type CloudContext = { client: CloudCpClient; orgId?: string };

const telemetryContext = (projectId: string) => ({
	source: "project-remove",
	operation: "project_remove",
	surface: "project_board",
	project_id: projectId,
});

function codedError(message: string, code?: string): Error & { code?: string } {
	return Object.assign(new Error(message), code ? { code } : {});
}

export function isRemoveTarget(workspace: WorkspaceSummary, projectId: string, hostId?: string, cloud?: boolean): boolean {
	return workspace.id === projectId && workspace.hostId === hostId &&
		(cloud === undefined || (workspace.kind === CLOUD_PROJECT_KIND) === cloud);
}

function reinsert<T extends { id: string }>(current: T[] | undefined, item: T, index: number): T[] | undefined {
	if (!current || current.some((candidate) => candidate.id === item.id)) return current;
	const next = [...current];
	next.splice(Math.min(index, next.length), 0, item);
	return next;
}

function serverStillLists(queryClient: QueryClient, { workspace, cloud, cacheKey }: RemoveTarget): boolean {
	if (cloud) {
		const listed = (lists: Array<[unknown, Array<{ id: string }> | undefined]>) =>
			lists.some(([, projects]) => projects?.some((project) => project.id === workspace.id) === true);
		return listed(queryClient.getQueriesData<CloudCpProject[]>({ queryKey: cloudProjectsQueryKey })) ||
			listed(queryClient.getQueriesData<WorkspaceSummary[]>({ queryKey: sharedCloudWorkspacesQueryKey }));
	}
	if (workspace.hostId) {
		const raw = queryClient.getQueryData<Array<{ id: string }>>(remoteProjectsQueryKey(workspace.hostId));
		if (raw?.some((project) => project.id === workspace.id)) return true;
	}
	return queryClient.getQueryData<WorkspaceSummary[]>(cacheKey)?.some((item) => item.id === workspace.id) === true;
}

async function refetchLists(queryClient: QueryClient, { cloud, workspace, cacheKey }: RemoveTarget): Promise<boolean> {
	const refresh = (queryKey: readonly unknown[], exact = false) => void queryClient.refetchQueries({ queryKey, exact });
	const read = (queryKey: readonly unknown[], exact = false) =>
		queryClient.refetchQueries({ queryKey, exact }, { throwOnError: true });
	try {
		if (cloud) {
			refresh(cloudSessionsQueryKey);
			await Promise.all([read(cloudProjectsQueryKey), read(sharedCloudWorkspacesQueryKey)]);
		} else if (workspace.hostId) {
			refresh(remoteSessionsQueryKey(workspace.hostId), true);
			refresh(cacheKey, true);
			await read(remoteProjectsQueryKey(workspace.hostId), true);
		} else {
			await read(cacheKey, true);
		}
		return true;
	} catch {
		return false;
	}
}

export function useRemoveProject({ findWorkspace, isViewing, cloud }: {
	findWorkspace: (projectId: string, hostId?: string, cloud?: boolean) => WorkspaceSummary | undefined;
	isViewing: (workspace: WorkspaceSummary) => boolean;
	cloud: CloudContext;
}): (projectId: string, hostId?: string, cloud?: boolean) => void {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const queryClient = useQueryClient();

	const remove = useLatestCallback((projectId: string, hostId?: string, cloudRow?: boolean): void => {
		const workspace = findWorkspace(projectId, hostId, cloudRow);
		if (!workspace || isOptimisticallyRemovedProject(projectId, hostId, !hostId && workspace.kind === CLOUD_PROJECT_KIND)) return;
		void removeOptimistically(workspace);
	});

	const sendRemove = async ({ workspace, cloud: isCloud }: RemoveTarget, orgId: string | undefined): Promise<void> => {
		if (isCloud) {
			try {
				await cloud.client.deleteProject(orgId ?? "", workspace.id);
			} catch (error) {
				throw codedError(error instanceof Error ? error.message : "Unable to delete cloud project");
			}
			return;
		}
		const client = workspace.hostId ? clientForHost(workspace.hostId) : apiClient;
		const { error } = await client.DELETE("/api/v1/projects/{id}", { params: { path: { id: workspace.id } } });
		if (error) throw Object.assign(apiErrorException(error), { code: apiErrorCode(error) });
	};

	const removeOptimistically = async (workspace: WorkspaceSummary): Promise<void> => {
		const { showGlobalToast } = useUiStore.getState();
		const { id: projectId, hostId, name } = workspace;
		const isCloud = !hostId && workspace.kind === CLOUD_PROJECT_KIND;
		const target: RemoveTarget = {
			workspace,
			cloud: isCloud,
			cacheKey: hostId ? remoteWorkspaceQueryKey(hostId) : workspaceQueryKey,
		};
		const toastKey = `project-remove:${isCloud ? `@cloud:${projectId}` : refKey({ host: hostId ?? LOCAL_HOST, id: projectId })}`;
		const showFailure = (message: string) => showGlobalToast(t("shell.couldNotRemoveProjectNamed", { name }), message, {
			tone: "error",
			dismissible: true,
			durationMs: PROJECT_REMOVE_ERROR_TOAST_MS,
			dedupeKey: toastKey,
			action: { label: t("shell.removeProjectRetry"), run: () => remove(projectId, hostId, isCloud) },
		});
		if (!hostId) {
			void addRendererExceptionStep("Project removal requested", telemetryContext(projectId));
		}
		const orgId = cloud.orgId;
		if (isCloud && !orgId) {
			const failure = codedError("The cloud control plane is not ready. Sign in and try again.", "cloud_not_ready");
			void captureRendererException(failure, telemetryContext(projectId));
			showFailure(failure.message);
			return;
		}

		if (isViewing(workspace)) {
			try {
				await navigate({ to: "/", replace: true });
			} catch {
			}
		}

		const previous = isCloud ? undefined : queryClient.getQueryData<WorkspaceSummary[]>(target.cacheKey);
		const snapshotIndex = previous?.findIndex((item) => item.id === projectId) ?? -1;
		const snapshot = snapshotIndex >= 0 ? previous?.[snapshotIndex] : undefined;
		const cloudSnapshots = isCloud ? queryClient.getQueriesData<CloudCpProject[]>({ queryKey: cloudProjectsQueryKey }) : [];
		trackOptimisticProjectRemoval(projectId, hostId, isCloud);
		queryClient.setQueryData<WorkspaceSummary[]>(target.cacheKey, (current) => current && applyOptimisticProjectRemovals(current));
		if (isCloud) {
			queryClient.setQueriesData<CloudCpProject[]>({ queryKey: cloudProjectsQueryKey }, (current) =>
				current?.some((project) => project.id === projectId) ? current.filter((project) => project.id !== projectId) : current);
		}
		showGlobalToast(isCloud ? t("shell.cloudProjectDeleted", { name }) : t("shell.projectRemoved", { name }), undefined, { tone: "success", dedupeKey: toastKey });

		const unpin = () => {
			if (!hostId) useSidebarOrderStore.getState().setProjectPinned(projectId, false);
		};

		try {
			await sendRemove(target, orgId);
		} catch (cause) {
			const failure = cause instanceof Error ? cause : new Error(t("shell.couldNotRemoveProject"));
			if (!hostId) void captureRendererException(failure, telemetryContext(projectId));
			clearOptimisticProjectRemoval(projectId, hostId, isCloud);
			const read = await refetchLists(queryClient, target);
			if (read && !serverStillLists(queryClient, target)) {
				unpin();
				return;
			}
			if (snapshot) {
				queryClient.setQueryData<WorkspaceSummary[]>(target.cacheKey, (current) => reinsert(current, snapshot, snapshotIndex));
			}
			for (const [queryKey, projects] of cloudSnapshots) {
				const index = projects?.findIndex((item) => item.id === projectId) ?? -1;
				const removed = index >= 0 ? projects?.[index] : undefined;
				if (removed) queryClient.setQueryData<CloudCpProject[]>(queryKey, (current) => reinsert(current, removed, index));
			}
			showFailure(failure.message || t("shell.couldNotRemoveProject"));
			return;
		}

		if (!hostId) void captureRendererEvent("ao.projects.removed", { project_id: projectId });
		unpin();
		const read = await refetchLists(queryClient, target);
		if (!isCloud && read && !serverStillLists(queryClient, target)) clearOptimisticProjectRemoval(projectId, hostId);
	};

	return remove;
}
