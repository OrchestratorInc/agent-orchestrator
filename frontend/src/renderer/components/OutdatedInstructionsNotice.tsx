import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { AlertTriangle, RotateCw } from "@/components/app-icons";
import { useCloudCp } from "../hooks/useCloudCp";
import { refreshCloudSessionLifecycle } from "../hooks/useWorkspaceQuery";
import { cn } from "../lib/utils";
import type { WorkspaceSession } from "../types/workspace";
import { Button } from "./ui/button";

/**
 * Says a Cloud session's agent runs with project instructions older than the
 * project's current ones (`outdatedInstructions`), and offers the restart that
 * applies them. The restart resumes the agent's conversation in place: it is
 * not the board's orchestrator replacement, which starts a new session. A
 * session whose worker can only apply them at its agent's next start gets the
 * notice without the action. Renders nothing for a session that is current.
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
	const restart = useMutation({
		mutationKey: ["restart-for-instructions", session.id],
		mutationFn: async () => {
			const orgId = session.cloud?.orgId;
			if (!orgId) return;
			const result = await client.restartSessionAgent(orgId, session.id, { applyInstructions: true });
			if (!result.requested) throw new Error(t("session.instructionsChanged.notRestarted"));
		},
		onSettled: () => refreshCloudSessionLifecycle(queryClient),
	});
	if (outdated !== "restart" && outdated !== "next_start") return null;

	// Restarting ends a turn in progress, so it waits until the agent is idle.
	const busy = session.activity?.state === "active";
	const heading = session.kind === "orchestrator"
		? t("session.instructionsChanged.orchestrator")
		: t("session.instructionsChanged.worker");
	const hint = outdated === "next_start"
		? t("session.instructionsChanged.nextStartHint")
		: busy
			? t("session.instructionsChanged.busyHint")
			: t("session.instructionsChanged.restartHint");
	const error = restart.error instanceof Error ? restart.error.message : null;
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
					disabled={busy || restart.isPending}
					onClick={() => restart.mutate()}
				>
					<RotateCw className={cn("size-3.5", restart.isPending && "animate-spin")} aria-hidden="true" />
					{t("session.instructionsChanged.restart")}
				</Button>
			) : null}
		</div>
	);
}
