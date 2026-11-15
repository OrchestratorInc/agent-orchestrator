import { apiErrorException } from "../lib/api-error-exception";
import { useCallback, useSyncExternalStore } from "react";
import { hashKey, type QueryClient, useMutation, useMutationState, useQueryClient } from "@tanstack/react-query";
import type { TFunction } from "i18next";
import { STANDALONE_WORKSPACE_ID, type WorkspaceSession, type WorkspaceSummary } from "../types/workspace";
import { cloudSessionsQueryKey, remoteSessionsQueryKey, workspaceQueryKey, workspaceQueryKeyForHost } from "./useWorkspaceQuery";
import {
	applyTerminatedSession,
	clearOptimisticSessionKill,
	trackOptimisticSessionKill,
} from "./optimistic-session-kills";
import { apiClient } from "../lib/api-client";
import { captureRendererEvent } from "../lib/telemetry";
import { createRendererCloudCpClient } from "./useCloudCp";
import { useTranslation } from "react-i18next";
import { settingsQueryKey, type Settings } from "./useSettings";
import type { CloudCpSession } from "../lib/cloud-cp";
import { clientForHost } from "../lib/host-clients";
import { LOCAL_HOST, refKey, sessionUiKey } from "../lib/hosts";
import { recordDirectWorkerInteraction } from "../lib/session-management-telemetry";
import { useUiStore } from "../stores/ui-store";

type TerminateSessionOptions = {
	onOptimistic?: (session: WorkspaceSession) => void;
	onSuccess?: (session: WorkspaceSession) => void;
};

export const terminateSessionMutationKey = ["terminate-session"] as const;

export type TerminateSessionRequest = WorkspaceSession & { deleteArtifacts?: boolean };

async function terminateSession(queryClient: QueryClient, session: TerminateSessionRequest): Promise<boolean> {
	if (session.cloud) {
		const settings = queryClient.getQueryData<Settings>(settingsQueryKey);
		const baseUrl = settings?.cloudControlPlaneUrl ?? "";
		if (baseUrl === "") throw new Error("The cloud control plane is not configured.");
		const client = createRendererCloudCpClient(baseUrl);
		if (!session.deleteArtifacts) {
			await client.deleteSession(session.cloud.orgId, session.id);
			return false;
		}
		const deleted = await client.deleteSession(session.cloud.orgId, session.id, { deleteArtifacts: true });
		return deleted.artifactsDeleted === true;
	}

	const { data, error, response } = await (session.hostId ? clientForHost(session.hostId) : apiClient).POST("/api/v1/sessions/{sessionId}/kill", {
		params: { path: { sessionId: session.id } },
		body: session.deleteArtifacts ? { deleteArtifacts: true } : undefined,
	});
	if (error) {
		const fallback = response ? `Failed to terminate session (${response.status})` : "Failed to terminate session";
		throw apiErrorException(error, fallback);
	}
	return data?.artifactsDeleted === true;
}

type WorkspaceSnapshot = [readonly unknown[], WorkspaceSummary[] | undefined];
type CloudSnapshot = [readonly unknown[], CloudCpSession[] | undefined];

function markCloudSessionTerminated(sessionId: string) {
	return (session: CloudCpSession): CloudCpSession =>
		session.id === sessionId ? { ...session, isTerminated: true, status: "terminated" } : session;
}

type TerminateMutationContext = {
	workspace: WorkspaceSnapshot | undefined;
	cloud: CloudSnapshot[];
};

type TerminateSessionMutationState = {
	error: unknown;
	session?: WorkspaceSession;
	status: "error" | "idle" | "pending" | "success";
	submittedAt: number;
};

function useTerminateSessionMutations() {
	return useMutationState<TerminateSessionMutationState>({
		filters: { mutationKey: terminateSessionMutationKey },
		select: (mutation) => ({
			error: mutation.state.error,
			session: mutation.state.variables as WorkspaceSession | undefined,
			status: mutation.state.status,
			submittedAt: mutation.state.submittedAt,
		}),
	});
}

function summarizeBySession(mutations: TerminateSessionMutationState[]) {
	const summaries = new Map<
		string,
		{ isPending: boolean; latest: TerminateSessionMutationState; session: WorkspaceSession }
	>();
	for (const mutation of mutations) {
		if (!mutation.session) continue;
		const key = refKey({ host: mutation.session.hostId ?? LOCAL_HOST, id: mutation.session.id });
		const current = summaries.get(key);
		if (!current) {
			summaries.set(key, {
				isPending: mutation.status === "pending",
				latest: mutation,
				session: mutation.session,
			});
			continue;
		}
		current.isPending ||= mutation.status === "pending";
		if (mutation.submittedAt >= current.latest.submittedAt) current.latest = mutation;
	}
	return [...summaries.values()];
}

export function useTerminateSession(options: TerminateSessionOptions = {}) {
	const queryClient = useQueryClient();
	const { t } = useTranslation();
	return useMutation({
		mutationKey: terminateSessionMutationKey,
		mutationFn: async (session: TerminateSessionRequest) => {
			recordDirectWorkerInteraction(session.id, "lifecycle", session.kind, session.hostId);

			return terminateSession(queryClient, session);
		},
			onMutate: async (session): Promise<TerminateMutationContext> => {
			options.onOptimistic?.(session);
			const queryKey = workspaceQueryKeyForHost(session.hostId);
			await queryClient.cancelQueries({ queryKey });
			const workspace: WorkspaceSnapshot = [
				queryKey,
				queryClient.getQueryData<WorkspaceSummary[]>(queryKey),
			];
			trackOptimisticSessionKill(session.id, session.hostId);
			queryClient.setQueryData<WorkspaceSummary[]>(queryKey, (workspaces) =>
				applyTerminatedSession(workspaces, session.id, session.hostId),
			);
			const cloud: CloudSnapshot[] = [];
			if (session.cloud) {
				await queryClient.cancelQueries({ queryKey: cloudSessionsQueryKey });
				for (const [key, sessions] of queryClient.getQueriesData<CloudCpSession[]>({
					queryKey: cloudSessionsQueryKey,
				})) {
					cloud.push([key, sessions]);
					if (!sessions?.some((s) => s.id === session.id)) continue;
					queryClient.setQueryData<CloudCpSession[]>(key, sessions.map(markCloudSessionTerminated(session.id)));
				}
			}
			return { workspace, cloud };
		},
		onSuccess: async (artifactsDeleted, session) => {
			useUiStore.getState().resetFilesSelection(sessionUiKey(session.id, session.hostId));
			void captureRendererEvent("ao.sessions.killed", { project_id: session.workspaceId });
			if (session.deleteArtifacts && !artifactsDeleted) {
				useUiStore.getState().showGlobalToast(t("archive.artifactsNotDeleted"), t("archive.artifactsNotDeletedBody"), "error");
			}
			const queryKey = workspaceQueryKeyForHost(session.hostId);
			queryClient.setQueryData<WorkspaceSummary[]>(queryKey, (workspaces) =>
				applyTerminatedSession(workspaces, session.id, session.hostId),
			);
			await queryClient.invalidateQueries({ queryKey });
			if (session.cloud) await queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey });
			else reportArchiveCleanup(queryClient, session, t);
			options.onSuccess?.(session);
		},
		onError: (_error, session, context) => {
			void captureRendererEvent("ao.sessions.kill_failed", { project_id: session.workspaceId });
			clearOptimisticSessionKill(session.id, session.hostId);
			const ctx = context as TerminateMutationContext | undefined;
			if (ctx?.workspace) queryClient.setQueryData(ctx.workspace[0], ctx.workspace[1]);
			for (const [key, sessions] of ctx?.cloud ?? []) queryClient.setQueryData(key, sessions);
		},
		onSettled: (_data, error, session) => {
			if (!error) clearOptimisticSessionKill(session.id, session.hostId);
		},
	});
}

type CleanupFacts = Pick<WorkspaceSession, "id" | "isTerminated" | "workspaceCleanup" | "workspaceCleanupReportOnly">;

function archiveCleanup(session: CleanupFacts | undefined): WorkspaceSession["workspaceCleanup"] {
	return session?.workspaceCleanupReportOnly ? undefined : session?.workspaceCleanup;
}

type WorkspaceCleanupErrorKey = "shell.workspaceCleanupFailed" | "shell.workspaceCleanupDirty";

function workspaceCleanupErrorKey(session: CleanupFacts | undefined): WorkspaceCleanupErrorKey | null {
	const cleanup = archiveCleanup(session);
	if (cleanup === "failed") return "shell.workspaceCleanupFailed";
	if (cleanup === "preserved_dirty") return "shell.workspaceCleanupDirty";
	return null;
}

const CLEANUP_REPORT_WINDOW_MS = 30 * 60_000;
const CLEANUP_NOTICE_MS = 10_000;

function showWorkspaceCleanupNotice(session: WorkspaceSession, errorKey: WorkspaceCleanupErrorKey, t: TFunction) {
	useUiStore.getState().showGlobalToast(t(errorKey), session.title, {
		tone: errorKey === "shell.workspaceCleanupFailed" ? "error" : "info",
		dismissible: true,
		durationMs: CLEANUP_NOTICE_MS,
		dedupeKey: `workspace-cleanup:${sessionUiKey(session.id, session.hostId)}`,
		...(session.workspaceId !== STANDALONE_WORKSPACE_ID && {
			action: { label: t("settings.project.retryCleanup"), run: () => void retryWorkspaceCleanup(session, t) },
		}),
	});
}

async function retryWorkspaceCleanup(session: WorkspaceSession, t: TFunction) {
	const { showGlobalToast } = useUiStore.getState();
	try {
		const { data, error } = await (session.hostId ? clientForHost(session.hostId) : apiClient).POST("/api/v1/sessions/cleanup", {
			params: { query: { project: session.workspaceId } },
		});
		if (error || !data) throw new Error("cleanup retry refused");
		const skip = data.skipped.find(({ sessionId }) => sessionId === session.id);
		if (skip) {
			showWorkspaceCleanupNotice(session, skip.code === "dirty" ? "shell.workspaceCleanupDirty" : "shell.workspaceCleanupFailed", t);
			return;
		}
		showGlobalToast(t("settings.project.cleanupRetryResult", {
			cleaned: data.cleaned.length + data.alreadyGone.length,
			skipped: data.skipped.length,
		}), undefined, "success");
	} catch {
		showGlobalToast(t("settings.project.cleanupRetryFailed"), undefined, "error");
	}
}

function reportArchiveCleanup(queryClient: QueryClient, session: WorkspaceSession, t: TFunction) {
	const queryKey = session.hostId ? remoteSessionsQueryKey(session.hostId) : workspaceQueryKey;
	const rows = (): CleanupFacts[] | undefined => session.hostId
		? queryClient.getQueryData<CleanupFacts[]>(queryKey)
		: queryClient.getQueryData<WorkspaceSummary[]>(queryKey)?.flatMap((workspace) => workspace.sessions);
	const settled = () => {
		const current = rows()?.find(({ id }) => id === session.id);
		if (!current?.isTerminated) return true;
		const cleanup = archiveCleanup(current);
		if (cleanup === undefined || cleanup === "pending") return false;
		const errorKey = workspaceCleanupErrorKey(current);
		if (errorKey) showWorkspaceCleanupNotice(session, errorKey, t);
		return true;
	};
	if (settled()) return;
	const stop = () => {
		unsubscribe();
		window.clearTimeout(timer);
	};
	const queryHash = hashKey(queryKey);
	const unsubscribe = queryClient.getQueryCache().subscribe((event) => {
		if (event.query.queryHash === queryHash && settled()) stop();
	});
	const timer = window.setTimeout(stop, CLEANUP_REPORT_WINDOW_MS);
}

const noSubscription = () => () => {};

function useCachedWorkspaces(hostId?: string, enabled = true) {
	const queryClient = useQueryClient();
	const subscribe = useCallback((notify: () => void) => queryClient.getQueryCache().subscribe(notify), [queryClient]);
	return useSyncExternalStore(
		enabled ? subscribe : noSubscription,
		() => (enabled ? queryClient.getQueryData<WorkspaceSummary[]>(workspaceQueryKeyForHost(hostId)) : undefined),
	);
}

export function useTerminateSessionState(sessionId: string, hostId?: string, enabled = true) {
	const data = useCachedWorkspaces(hostId, enabled);
	const session = data?.flatMap((workspace) => workspace.sessions).find((session) => session.id === sessionId);
	const summary = summarizeBySession(useTerminateSessionMutations()).find(({ session }) =>
		session.id === sessionId && session.hostId === hostId);
	return {
		error: !summary?.isPending && summary?.latest.status === "error" && summary.latest.error instanceof Error
			? summary.latest.error.message : null,
		isPending: summary?.isPending === true || archiveCleanup(session) === "pending",
	};
}

export function useProjectTerminateSessionStates(workspaceId: string | undefined, hostId?: string) {
	const data = useCachedWorkspaces(hostId);
	const sessions = data?.find((workspace) => workspace.id === workspaceId)?.sessions ?? [];
	const states = summarizeBySession(useTerminateSessionMutations())
		.filter(({ session }) => session.hostId === hostId && session.workspaceId === workspaceId)
		.sort((a, b) => b.latest.submittedAt - a.latest.submittedAt)
		.map(({ isPending, latest, session }) => ({
			error: !isPending && latest.error instanceof Error ? latest.error.message : null,
			isPending, session, cleanupPending: false,
		}));
	for (const session of sessions) {
		if (archiveCleanup(session) !== "pending") continue;
		const previous = states.findIndex((state) => state.session.id === session.id);
		const state = { error: null, isPending: true, session, cleanupPending: true };
		if (previous < 0) states.push(state);
		else states[previous] = state;
	}
	return states.filter((state) => state.error || state.isPending);
}

export function clearTerminateSessionState(queryClient: QueryClient, sessionId: string, hostId?: string) {
	const mutationCache = queryClient.getMutationCache();
	for (const mutation of mutationCache.findAll({ mutationKey: terminateSessionMutationKey })) {
		const target = mutation.state.variables as WorkspaceSession | undefined;
		if (target?.id === sessionId && target.hostId === hostId && mutation.state.status !== "pending") {
			mutationCache.remove(mutation);
		}
	}
}
