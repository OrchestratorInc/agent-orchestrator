import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { clientForSessionHost, labelForHost } from "../lib/host-clients";
import { LOCAL_HOST, type HostId } from "../lib/hosts";
import { askMove, clearMove, moveKey, moveShown, runMove, useMoveStore, type MoveTarget } from "../lib/move-runner";
import { cancelMove, commitMove, type MoveResult } from "../lib/move-session";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import { projectsWithRepo } from "../lib/repo-hosts";
import { useUiStore } from "../stores/ui-store";
import type { WorkspaceSession } from "../types/workspace";
import { useConnectedHosts } from "./useHostConnection";
import { useRemoteWorkspaces, useWorkspaceQuery, workspaceQueryKeyForHost } from "./useWorkspaceQuery";

/**
 * The other computers' projects for this session's repository; a computer can
 * hold it more than once. Disabled, it reads only what is already cached, so a
 * closed menu neither fetches nor subscribes. settled: every list has answered.
 */
export function useMoveTargets(session: WorkspaceSession | undefined, enabled = true) {
	const { t } = useTranslation();
	const on = enabled && Boolean(session);
	const localQuery = useWorkspaceQuery({ enabled: on, subscribed: on, includeCloud: false });
	const remoteQuery = useRemoteWorkspaces({ enabled: on, subscribed: on });
	const connected = useConnectedHosts();
	const local = localQuery.data ?? [];
	const remote = remoteQuery.data ?? [];
	const answered = new Set([...remoteQuery.loadedProjectHostIds, ...remoteQuery.failedHostIds]);
	const settled = (localQuery.data !== undefined || localQuery.isError) && connected.every((hostId) => answered.has(hostId));
	if (!session) return { targets: [], settled };
	const sourceHost = session.hostId ?? LOCAL_HOST;
	const workspaces = sourceHost === LOCAL_HOST ? local : remote.filter((workspace) => workspace.hostId === sourceHost);
	const repoKey = workspaces.find((workspace) => workspace.id === session.workspaceId)?.repoKey;
	const targets: (MoveTarget & { projectName: string })[] = projectsWithRepo(local, remote, repoKey)
		.filter((project) => project.hostId !== sourceHost)
		.map((project) => ({ ...project, label: project.hostId === LOCAL_HOST ? t("remote.thisComputer") : labelForHost(project.hostId) ?? project.hostId }));
	return { targets, settled };
}

/** Starts, finishes and settles a session's move: the menu, the banner and the unfinished-move strip share it. */
export function useMoveSession(session: WorkspaceSession | undefined) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	if (!session) return undefined;
	const key = moveKey(session.hostId, session.id);
	const sourceHost: HostId = session.hostId ?? LOCAL_HOST;
	// Clients are made only when a move runs: an offline host's client throws.
	const sourceLabel = sourceHost === LOCAL_HOST ? t("remote.thisComputer") : labelForHost(sourceHost) ?? sourceHost;
	const source = () => ({ client: clientForSessionHost(session.hostId), hostId: sourceHost, sessionId: session.id, label: sourceLabel });
	const destination = (target: MoveTarget) => ({
		client: clientForSessionHost(target.hostId === LOCAL_HOST ? undefined : target.hostId),
		hostId: target.hostId,
		label: target.label,
		projectId: target.projectId,
	});
	const refresh = (hostId: HostId) => {
		void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(session.hostId) });
		void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId === LOCAL_HOST ? undefined : hostId) });
		void queryClient.invalidateQueries({ queryKey: ["session-moved-to"] });
	};
	const open = (target: MoveTarget, sessionId: string) => void navigate(sessionNavigateTarget(target.projectId, sessionId, target.hostId));

	/** An offline host has no client, so the click says so instead of doing nothing. */
	const connected = <T>(label: string, make: () => T): T | undefined => {
		try {
			return make();
		} catch {
			showGlobalToast(t("moveSession.toastFailed", { title: session.title }), `${label} is not reachable`, "error");
			return undefined;
		}
	};

	const start = (target: MoveTarget, options: { afterTurn?: boolean; stopTurn?: boolean } = {}) => {
		const from = connected(sourceLabel, source);
		const to = from && connected(target.label, () => destination(target));
		if (!from || !to) return;
		void runMove(key, { source: from, target: to }, {
			...options,
			title: session.title,
			onDone: (result: MoveResult) => {
				refresh(target.hostId);
				// Not marked moved here yet: the banner offers Retry; away from it, say so.
				if (!result.committed) {
					if (!moveShown(key)) showGlobalToast(t("moveSession.toastFailed", { title: session.title }), result.commitError, "error");
					return;
				}
				clearMove(key);
				const title = t("moveSession.toastDone", { host: target.label });
				const summary = result.resumed ? undefined : t("moveSession.summaryBody");
				// Watching the session: follow it there. Elsewhere: a toast that can open it.
				if (moveShown(key)) {
					open(target, result.sessionId);
					showGlobalToast(title, summary, "success");
				} else {
					showGlobalToast(title, summary ?? session.title, { tone: "success", action: { label: t("moveSession.open"), run: () => open(target, result.sessionId) } });
				}
			},
			onFail: (error) => {
				refresh(target.hostId);
				if (!moveShown(key)) showGlobalToast(t("moveSession.toastFailed", { title: session.title }), error.message, "error");
			},
		});
	};
	const target = (): MoveTarget | undefined => {
		const move = useMoveStore.getState().moves[key];
		return move && { hostId: move.targetHostId, projectId: move.targetProjectId, label: move.targetLabel };
	};

	return {
		key,
		start,
		/** Moves now, or asks first when Claude is in the middle of a turn. */
		choose: (picked: MoveTarget) => {
			const state = session.activity?.state;
			if (state === "active" || state === "blocked") askMove(key, picked, session.title);
			else start(picked);
		},
		retry: (options: { afterTurn?: boolean; stopTurn?: boolean } = {}) => {
			const picked = target();
			if (picked) start(picked, options);
		},
		/** Marks the source moved after only that last step failed. */
		retryCommit: async () => {
			const move = useMoveStore.getState().moves[key];
			const picked = target();
			const result = move?.result;
			if (!picked || !result) return;
			const from = connected(sourceLabel, source);
			if (!from) return;
			const commitError = await commitMove(from, { hostId: picked.hostId, label: result.destinationLabel }, result.transferId, result.sessionId);
			if (commitError) {
				useMoveStore.setState((state) => ({ moves: { ...state.moves, [key]: { ...state.moves[key], result: { ...result, commitError } } } }));
				return;
			}
			clearMove(key);
			refresh(picked.hostId);
			open(picked, result.sessionId);
		},
		/** Drops an unconfirmed move so the session runs here again. */
		keepHere: async (transferId: string | undefined) => {
			const from = transferId ? connected(sourceLabel, source) : undefined;
			if (transferId && !from) return;
			if (transferId && from) await cancelMove(from, transferId);
			clearMove(key);
			refresh(target()?.hostId ?? LOCAL_HOST);
		},
		dismiss: () => clearMove(key),
	};
}
