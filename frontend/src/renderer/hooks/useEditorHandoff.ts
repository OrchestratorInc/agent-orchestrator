import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef } from "react";
import { isEditorId, type EditorHandoffState, type OpenTargetId } from "../../shared/editor-handoff";
import { aoBridge } from "../lib/bridge";
import { captureRendererEvent } from "../lib/telemetry";

export const editorHandoffQueryKey = (sessionId: string) => ["editor-handoff", sessionId] as const;
export const editorHandoffQueryRoot = ["editor-handoff"] as const;

const NEW_SESSION_READINESS_WINDOW_MS = 30_000;
const WORKSPACE_READINESS_RETRY_MS = 500;
const WORKSPACE_READINESS_MAX_RETRIES = 10;
const WORKSPACE_CHECK_RETRY_MS = 5_000;
const WORKSPACE_CHECK_MAX_FAILURES = 3;

type EditorHandoffReadiness = {
	sessionCreatedAt?: string;
	sessionTerminated?: boolean;
};

function shouldAwaitWorkspace({ sessionCreatedAt, sessionTerminated }: EditorHandoffReadiness): boolean {
	if (sessionTerminated || !sessionCreatedAt) return false;
	const createdAt = Date.parse(sessionCreatedAt);
	if (!Number.isFinite(createdAt)) return false;
	const age = Date.now() - createdAt;
	return age >= 0 && age <= NEW_SESSION_READINESS_WINDOW_MS;
}

function waitForWorkspaceRetry(signal: AbortSignal): Promise<void> {
	return new Promise((resolve, reject) => {
		if (signal.aborted) {
			reject(signal.reason ?? new DOMException("Workspace readiness check cancelled", "AbortError"));
			return;
		}
		const onAbort = () => {
			clearTimeout(timer);
			reject(signal.reason ?? new DOMException("Workspace readiness check cancelled", "AbortError"));
		};
		const timer = setTimeout(() => {
			signal.removeEventListener("abort", onAbort);
			resolve();
		}, WORKSPACE_READINESS_RETRY_MS);
		signal.addEventListener("abort", onAbort, { once: true });
	});
}

// Electron wraps anything an ipcMain handler throws as
// "Error invoking remote method '<channel>': Error: <real message>". That prefix
// is developer noise, and the topbar renders the message verbatim, so strip it
// and surface only what the main process actually said.
const IPC_WRAPPER = /^Error invoking remote method '[^']*':\s*(?:[A-Za-z]*Error:\s*)?/;

export function editorHandoffErrorMessage(error: unknown): string | null {
	if (!(error instanceof Error)) return null;
	const message = error.message.replace(IPC_WRAPPER, "").trim();
	return message || error.message;
}

/** The daemon failed while checking, which says nothing about whether the worktree exists. */
export function workspaceCheckFailed(state: EditorHandoffState | undefined): boolean {
	return state?.workspaceAvailable === false &&
		(state.unavailableCode === "INTERNAL_ERROR" || state.unavailableCode === "SERVICE_UNAVAILABLE");
}

export function useEditorHandoffState(sessionId: string, readiness: EditorHandoffReadiness = {}) {
	const awaitWorkspace = shouldAwaitWorkspace(readiness);
	const failedChecks = useRef(0);
	return useQuery<EditorHandoffState>({
		queryKey: editorHandoffQueryKey(sessionId),
		enabled: Boolean(sessionId),
		staleTime: 10_000,
		retry: false,
		refetchInterval: (query) => (workspaceCheckFailed(query.state.data) ? WORKSPACE_CHECK_RETRY_MS : false),
		queryFn: async ({ signal }) => {
			// A missing workspace is returned as successful state rather than an
			// exception, so TanStack's retry option cannot recover it. Poll only
			// during the bounded window in which a newly created session can still
			// be crossing the daemon/UI readiness boundary.
			for (let retries = 0; ; retries += 1) {
				const state = await aoBridge.editorHandoff.getState(sessionId);
				if (state.workspaceAvailable || !awaitWorkspace || retries >= WORKSPACE_READINESS_MAX_RETRIES) {
					if (!workspaceCheckFailed(state)) {
						failedChecks.current = 0;
						return state;
					}
					// After a few failed checks stop treating it as transient: polling ends and the reason shows.
					failedChecks.current += 1;
					return failedChecks.current < WORKSPACE_CHECK_MAX_FAILURES ? state : { ...state, unavailableCode: undefined };
				}
				await waitForWorkspaceRetry(signal);
			}
		},
	});
}

export type OpenSessionTargetMutationInput = {
	sessionId: string;
	projectId: string;
	targetId?: OpenTargetId;
};

export function useOpenSessionTarget() {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: async ({ sessionId, projectId, targetId }: OpenSessionTargetMutationInput) => {
			void captureRendererEvent("ao.renderer.open_in_editor_requested", {
				project_id: projectId,
				target_kind: targetId === "file-manager" ? "file_manager" : targetId === "terminal" ? "terminal" : "editor",
				...(targetId && isEditorId(targetId) ? { editor_id: targetId } : {}),
			});
			try {
				return await aoBridge.editorHandoff.open({ sessionId, ...(targetId ? { targetId } : {}) });
			} catch (error) {
				// Normalize here, once, so every consumer of this mutation gets the
				// reason rather than the IPC wrapper. Callers render error.message
				// directly and should not each have to strip it.
				throw new Error(editorHandoffErrorMessage(error) ?? String(error));
			}
		},
		onSuccess: (result, input) => {
			if (result.kind === "editor" && isEditorId(result.id)) {
				queryClient.setQueryData<EditorHandoffState>(editorHandoffQueryKey(input.sessionId), (state) =>
					state ? { ...state, preferredEditorId: result.id as typeof state.preferredEditorId } : state,
				);
			}
			void captureRendererEvent("ao.renderer.open_in_editor_succeeded", {
				project_id: input.projectId,
				target_kind: result.kind,
				...(result.kind === "editor" ? { editor_id: result.id } : {}),
			});
		},
		onError: (_error, input) => {
			// The usual cause is the worktree going away after the cached state was
			// read (session killed, merged, cleaned up). Refetch so the control
			// disables itself instead of inviting the same failing click again.
			void queryClient.invalidateQueries({ queryKey: editorHandoffQueryKey(input.sessionId) });
			void captureRendererEvent("ao.renderer.open_in_editor_failed", { project_id: input.projectId });
		},
	});
}
