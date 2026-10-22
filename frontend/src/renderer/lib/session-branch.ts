import { STANDALONE_WORKSPACE_ID, type WorkspaceSession } from "../types/workspace";

export function sessionBranchPresentation(session?: WorkspaceSession): { branch?: string; naming: boolean } {
	// Keep a provisional branch private, including when creation fails before
	// the worktree step completes, and while the daemon's branch step applies
	// the generated name after launch.
	const launched = session?.provisionState !== "provisioning" && session?.provisionState !== "failed"
		&& Boolean(session?.terminalHandleId || session?.chatProviderPreserved);
	const unfinished = session && !launched && !session.cloud && session.kind !== "orchestrator"
		&& session.workspaceId !== STANDALONE_WORKSPACE_ID
		&& (session.provisionSteps?.some((step) => step.id === "worktree" && step.status !== "done")
			|| (!session.provisionSteps?.length && (session.provisionState === "provisioning" || session.provisionState === "failed")));
	const pendingName = Boolean(session && !session.isTerminated
		&& session.provisionSteps?.some((step) => step.id === "branch" && step.status === "running"));
	return {
		branch: unfinished || pendingName ? undefined : session?.branch,
		naming: pendingName || Boolean(unfinished && session?.provisionState !== "failed" && !session?.isTerminated
			&& (session?.provisionState === "provisioning"
				|| (session?.activity?.state !== "exited" && session?.statusReadiness !== "unavailable"))),
	};
}
