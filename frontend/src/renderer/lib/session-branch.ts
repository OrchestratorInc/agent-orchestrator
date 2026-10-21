import { STANDALONE_WORKSPACE_ID, type WorkspaceSession } from "../types/workspace";

export function sessionBranchPresentation(session?: WorkspaceSession): { branch?: string; naming: boolean } {
	// Worktree completion follows naming and persistence. Keep its provisional
	// branch private, including when creation fails before that step completes.
	const launched = session?.provisionState !== "provisioning" && session?.provisionState !== "failed"
		&& Boolean(session?.terminalHandleId || session?.chatProviderPreserved);
	const unfinished = session && !launched && !session.cloud && session.kind !== "orchestrator"
		&& session.workspaceId !== STANDALONE_WORKSPACE_ID
		&& (session.provisionSteps?.some((step) => step.id === "worktree" && step.status !== "done")
			|| (!session.provisionSteps?.length && (session.provisionState === "provisioning" || session.provisionState === "failed")));
	return {
		branch: unfinished ? undefined : session?.branch,
		naming: Boolean(unfinished && session?.provisionState !== "failed" && !session?.isTerminated
			&& (session?.provisionState === "provisioning"
				|| (session?.activity?.state !== "exited" && session?.statusReadiness !== "unavailable"))),
	};
}
