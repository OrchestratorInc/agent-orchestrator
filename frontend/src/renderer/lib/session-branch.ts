import { STANDALONE_WORKSPACE_ID, type WorkspaceSession } from "../types/workspace";

// The daemon renames a task's temporary branch in the background after launch,
// within its 20 s naming deadline.
const TASK_NAMING_WINDOW_MS = 30_000;

function awaitingTaskName(session: WorkspaceSession): boolean {
	const temporary = [`ao/${session.id}/root`, `ao/dev/${session.id}/root`, `ao/${session.id}`, `ao/dev/${session.id}`];
	return session.kind !== "orchestrator" && !session.isTerminated && temporary.includes(session.branch ?? "")
		&& Date.now() - Date.parse(session.createdAt ?? "") < TASK_NAMING_WINDOW_MS;
}

export function sessionBranchPresentation(session?: WorkspaceSession): { branch?: string; naming: boolean } {
	// Keep a provisional branch private, including when creation fails before
	// the worktree step completes, and while its generated name is on the way.
	const launched = session?.provisionState !== "provisioning" && session?.provisionState !== "failed"
		&& Boolean(session?.terminalHandleId || session?.chatProviderPreserved);
	const unfinished = session && !launched && !session.cloud && session.kind !== "orchestrator"
		&& session.workspaceId !== STANDALONE_WORKSPACE_ID
		&& (session.provisionSteps?.some((step) => step.id === "worktree" && step.status !== "done")
			|| (!session.provisionSteps?.length && (session.provisionState === "provisioning" || session.provisionState === "failed")));
	const pendingName = Boolean(session && !session.cloud && session.provisionState !== "failed" && awaitingTaskName(session));
	return {
		branch: unfinished || pendingName ? undefined : session?.branch,
		naming: pendingName || Boolean(unfinished && session?.provisionState !== "failed" && !session?.isTerminated
			&& (session?.provisionState === "provisioning"
				|| (session?.activity?.state !== "exited" && session?.statusReadiness !== "unavailable"))),
	};
}
