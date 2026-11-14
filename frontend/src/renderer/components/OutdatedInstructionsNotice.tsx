import { useMutation, useMutationState, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { AlertTriangle, RotateCw } from "@/components/app-icons";
import { useCloudCp } from "../hooks/useCloudCp";
import { refreshCloudSessionLifecycle } from "../hooks/useWorkspaceQuery";
import { CloudCpError } from "../lib/cloud-cp/errors";
import { cn } from "../lib/utils";
import type { WorkspaceSession } from "../types/workspace";
import { Button } from "./ui/button";

/**
 * How long after a restart that timed out the session is read again: the
 * worker may still finish the restart after the control plane stopped
 * waiting for it.
 */
const RESTART_TIMEOUT_REFRESH_MS = 10_000;

/**
 * A restart the control plane stopped waiting for (504), or whose answer was
 * lost on the way: the worker may still be restarting the agent.
 */
function restartMayStillComplete(error: unknown): boolean {
	return error instanceof CloudCpError && (error.status === 504 || error.status === 0);
}

/**
 * Says a Cloud session's agent runs with project instructions older than the
 * project's current ones (`outdatedInstructions`), and offers the restart that
 * applies them. The restart resumes the agent's conversation in place: it is
 * not the board's orchestrator replacement, which starts a new session. A
 * session whose worker cannot apply them while it runs gets the notice
 * without the action. Renders nothing for a session that is current.
 */
export function OutdatedInstructionsNotice({
	session,
	variant,
}: {
	session: WorkspaceSession;
	/** "board": the project board's orchestrator banner. "terminal": a strip above the agent terminal. */
	variant: "board" | "terminal";
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const outdated = session.cloud?.outdatedInstructions;
	const { client } = useCloudCp(Boolean(outdated));
	// One restart per session, whichever notice (the board's or the terminal's)
	// started it: both show it pending and its outcome.
	const mutationKey = ["restart-for-instructions", session.id];
	const restart = useMutation({
		mutationKey,
		mutationFn: async () => {
			const orgId = session.cloud?.orgId;
			if (!orgId) return;
			const result = await client.restartSessionAgent(orgId, session.id, { applyInstructions: true });
			if (!result.requested) throw new Error(t("session.instructionsChanged.notRestarted"));
		},
		onError: (error) => {
			if (!restartMayStillComplete(error)) return;
			setTimeout(() => void refreshCloudSessionLifecycle(queryClient), RESTART_TIMEOUT_REFRESH_MS);
		},
		onSettled: () => refreshCloudSessionLifecycle(queryClient),
	});
	const latest = useMutationState({
		filters: { mutationKey, exact: true },
		select: (mutation) => ({ status: mutation.state.status, error: mutation.state.error }),
	}).at(-1);
	if (outdated !== "restart" && outdated !== "next_start") return null;

	// Restarting ends a turn in progress, or a request the agent waits on (an
	// approval, a question), so it waits until the agent is idle.
	const activity = session.activity?.state;
	const working = activity === "active";
	const waiting = activity === "waiting_input" || activity === "blocked";
	const pending = latest?.status === "pending";
	const heading = session.kind === "orchestrator"
		? t("session.instructionsChanged.orchestrator")
		: t("session.instructionsChanged.worker");
	const hint = outdated === "next_start"
		? t("session.instructionsChanged.nextStartHint")
		: working
			? t("session.instructionsChanged.busyHint")
			: waiting
				? t("session.instructionsChanged.waitingHint")
				: t("session.instructionsChanged.restartHint");
	const failure = latest?.status === "error" ? latest.error : null;
	const error = restartMayStillComplete(failure)
		? t("session.instructionsChanged.mayStillRestart")
		: failure instanceof Error ? failure.message : null;
	return (
		<div
			className={cn(
				"flex flex-wrap items-center gap-3 text-xs text-muted-foreground",
				variant === "board"
					? "mx-3 my-3 rounded-md border border-border bg-surface px-3 py-2"
					: "shrink-0 border-b border-border bg-surface/80 px-4 py-2",
			)}
			data-testid="outdated-instructions-notice"
			role="status"
		>
			<AlertTriangle className="size-icon-base shrink-0 text-warning" aria-hidden="true" />
			<span className="min-w-0 flex-1">
				{heading} {hint}
				{error ? <span className="block text-error">{error}</span> : null}
			</span>
			{outdated === "restart" ? (
				<Button
					type="button"
					size="sm"
					variant={variant === "board" ? "primary" : "secondary"}
					disabled={working || waiting || pending}
					onClick={() => {
						// A second click, here or on the other notice, before this
						// one re-renders: the restart already runs.
						if (queryClient.isMutating({ mutationKey, exact: true }) > 0) return;
						restart.mutate();
					}}
				>
					<RotateCw className={cn("size-3.5", pending && "animate-spin")} aria-hidden="true" />
					{t("session.instructionsChanged.restart")}
				</Button>
			) : null}
		</div>
	);
}
