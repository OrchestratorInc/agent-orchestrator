import { useEffect, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { apiClient, apiErrorCode, apiErrorMessage } from "../lib/api-client";
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

/** drain: let the current turn finish first. interrupt: Ctrl-C, stop now. */
export type PausePolicy = "drain" | "interrupt";

/** The daemon could not prove the turn ended (agent waiting on a decision,
 * idleness unverifiable); only an interrupt will stop it. */
export const pauseDrainBlockedCode = "AGENT_PAUSE_DRAIN_BLOCKED";

/** Pause (exit-agent) or resume (resume-agent) one session's agent.
 * `memoryBytes` is the reading at click time, reported back as what the
 * pause freed. */
export function useAgentPause(session: WorkspaceSession, memoryBytes?: number) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const paused = isAgentPaused(session);
	const [freedBytes, setFreedBytes] = useState<number | undefined>();
	const [policy, setPolicy] = useState<PausePolicy | undefined>();
	const [drainBlocked, setDrainBlocked] = useState(false);
	const mutation = useMutation({
		mutationFn: async (variables: { policy?: PausePolicy }): Promise<number | undefined> => {
			if (paused) {
				const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/resume-agent", {
					params: { path: { sessionId: session.id } },
				});
				if (error) throw new Error(apiErrorMessage(error, t("session.resumeFailed")));
				return undefined;
			}
			const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/exit-agent", {
				params: { path: { sessionId: session.id } },
				body: { policy: variables.policy ?? "drain" },
			});
			if (error) {
				if (apiErrorCode(error) === pauseDrainBlockedCode) setDrainBlocked(true);
				throw new Error(apiErrorMessage(error, t("session.pauseFailed")));
			}
			return memoryBytes;
		},
		onMutate: (variables) => setPolicy(variables.policy),
		onSuccess: (freed) => {
			setFreedBytes(freed);
			setDrainBlocked(false);
		},
		onSettled: () => {
			setPolicy(undefined);
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
			void queryClient.invalidateQueries({ queryKey: sessionMemoryQueryRoot });
		},
	});
	useEffect(() => {
		if (freedBytes === undefined) return;
		const timer = setTimeout(() => setFreedBytes(undefined), pauseFreedNoticeMs);
		return () => clearTimeout(timer);
	}, [freedBytes]);
	return {
		paused,
		/** Resume, or pause with the given policy (drain when omitted). */
		toggle: (pausePolicy?: PausePolicy) => mutation.mutate({ policy: pausePolicy }),
		isPending: mutation.isPending,
		/** Set while a drained pause is waiting for the turn to end. */
		isDraining: mutation.isPending && !paused && policy !== "interrupt",
		/** The last drained pause was refused; offer interrupt. */
		drainBlocked,
		error: mutation.error,
		freedBytes,
	};
}
