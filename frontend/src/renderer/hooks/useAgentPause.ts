import { useEffect, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { isOrchestratorSession, type WorkspaceSession } from "../types/workspace";
import { sessionMemoryQueryRoot } from "./useSessionMemory";
import { workspaceQueryKey } from "./useWorkspaceQuery";

/** A paused agent was stopped on purpose: the process is gone and its memory
 * freed, the worktree, branch and conversation stay, and the daemon recorded
 * the intent. An agent that merely exited (crashed) is not paused; the
 * card's existing retry path covers that. */
export function isAgentPaused(session: WorkspaceSession): boolean {
	return session.pausedAt != null && session.isTerminated !== true;
}

/** Pause is offered for a running agent, resume for a paused one. A crashed
 * agent (exited, no pause record) gets neither: the card's status retry owns it. */
export function canPauseAgent(session: WorkspaceSession): boolean {
	if (session.isTerminated === true || isOrchestratorSession(session) || session.cloud) return false;
	return isAgentPaused(session) || session.activity?.state !== "exited";
}

/** How long "Freed 612 MB" stays on the card after a pause. */
export const pauseFreedNoticeMs = 4_000;

/** Pause (exit-agent) or resume (resume-agent) one session's agent.
 * `memoryBytes` is the reading at click time, reported back as what the
 * pause freed. */
export function useAgentPause(session: WorkspaceSession, memoryBytes?: number) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const paused = isAgentPaused(session);
	const [freedBytes, setFreedBytes] = useState<number | undefined>();
	const mutation = useMutation({
		mutationFn: async (): Promise<number | undefined> => {
			const path = paused
				? "/api/v1/sessions/{sessionId}/resume-agent"
				: "/api/v1/sessions/{sessionId}/exit-agent";
			const { error } = await apiClient.POST(path, { params: { path: { sessionId: session.id } } });
			if (error) {
				throw new Error(apiErrorMessage(error, paused ? t("session.resumeFailed") : t("session.pauseFailed")));
			}
			return paused ? undefined : memoryBytes;
		},
		onSuccess: (freed) => setFreedBytes(freed),
		onSettled: () => {
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
			void queryClient.invalidateQueries({ queryKey: sessionMemoryQueryRoot });
		},
	});
	useEffect(() => {
		if (freedBytes === undefined) return;
		const timer = setTimeout(() => setFreedBytes(undefined), pauseFreedNoticeMs);
		return () => clearTimeout(timer);
	}, [freedBytes]);
	return { paused, toggle: () => mutation.mutate(), isPending: mutation.isPending, error: mutation.error, freedBytes };
}
