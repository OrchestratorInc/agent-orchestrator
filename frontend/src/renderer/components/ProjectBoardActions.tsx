import { type CSSProperties } from "react";
import { useTranslation } from "react-i18next";
import { Plus } from "@/components/app-icons";
import type { ProjectOrchestratorAction } from "../hooks/useProjectOrchestratorAction";
import { getAgentActivityView } from "../lib/session-presentation";
import { useOrchestratorReplyState } from "../stores/orchestrator-reply-store";
import { Button } from "./ui/button";
import { TopbarActionError } from "./TopbarActionError";
import { OrchestratorReplyIcon, orchestratorReplyLabel } from "./OrchestratorReplyIcon";
import { captureTopbarAction } from "../lib/topbar-telemetry";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

export function ProjectBoardActions({ actions, placement, quiet = false, cloud = false, style }: {
	actions: ProjectOrchestratorAction;
	placement: "header" | "empty";
	quiet?: boolean;
	cloud?: boolean;
	style?: CSSProperties;
}) {
	const { t } = useTranslation();
	const { projectId, hostId, orchestrator, isSpawning, isProjectRestarting, isProvisioning, spawnError, canCreateAsTui,
		openNewTask, openOrchestrator } = actions;
	const header = placement === "header";
	const orchestratorReply = useOrchestratorReplyState({ projectId, hostId, orchestrator, viewing: false });
	const busy = isSpawning || isProjectRestarting || isProvisioning;
	const activity = orchestrator ? getAgentActivityView(orchestrator.activity, t).label : undefined;
	const actionLabel = orchestrator ? t("shell.openOrchestrator") : t("shell.spawnOrchestrator");
	const busyLabel = isProjectRestarting ? t("shell.restartingDots") : isProvisioning
		? t("shell.provisioningDots", { defaultValue: "Setting up…" }) : isSpawning ? t("shell.spawningDots") : undefined;
	// The header shows Orchestrator quiet, labeled, first. The empty board's
	// body keeps it as the call to action.
	const orchestratorVariant = header || quiet ? "quiet" : "primary";
	const orchestratorButton = (
		<Tooltip>
			<TooltipTrigger asChild>
				<span className="inline-flex" style={style}>
					<Button type="button" size="sm"
						aria-label={orchestratorReplyLabel(activity ? t("shell.orchestratorWithActivity", { activity }) : actionLabel, activity && orchestratorReply === "working" ? null : orchestratorReply, t)}
						aria-busy={busy}
						data-priority={header ? "secondary" : undefined}
						disabled={busy}
						onClick={() => {
							if (header) captureTopbarAction("open_orchestrator", "board");
							openOrchestrator();
						}}
						variant={orchestratorVariant}
					>
						<OrchestratorReplyIcon
							className="size-icon-md"
							reply={orchestratorReply}
							surface={orchestratorVariant === "primary" ? "filled" : "topbar"}
						/>
						<span data-compact-label={header ? "" : undefined}>{busyLabel ?? t("shell.orchestrator")}</span>
					</Button>
				</span>
			</TooltipTrigger>
			<TooltipContent side="bottom">{busyLabel ?? orchestratorReplyLabel(actionLabel, orchestratorReply, t)}</TooltipContent>
		</Tooltip>
	);
	const newTaskButton = (
		<Tooltip>
			<TooltipTrigger asChild>
				<span className="inline-flex" style={style}>
					<Button type="button" size="sm"
						aria-label={t("shell.newTask")}
						data-priority={header ? "primary" : undefined}
						disabled={isProjectRestarting || isProvisioning}
						onClick={() => {
							if (header) captureTopbarAction("new_task", "board");
							openNewTask();
						}}
						// The header's one filled action is New task.
						variant={header ? "primary" : quiet || cloud ? "quiet" : "secondary"}
					>
						<Plus className="size-icon-md" aria-hidden="true" />
						<span data-compact-label={header ? "" : undefined}>{t(header ? "newTask.task" : "shell.newTask")}</span>
					</Button>
				</span>
			</TooltipTrigger>
			<TooltipContent shortcut="new-session" side="bottom">{t("shell.newTask")}</TooltipContent>
		</Tooltip>
	);
	const feedback = spawnError && !quiet ? (
		<div className={header ? "contents" : "mt-3 flex flex-col items-center gap-2"}>
			<TopbarActionError role={header ? "alert" : "status"} className={header ? "max-w-content-max truncate" : "text-caption leading-body"} title={spawnError}>
				{spawnError}
			</TopbarActionError>
			{canCreateAsTui ? <Button type="button" size="sm" disabled={busy} onClick={() => openOrchestrator("tui")} style={style} variant="primary">{t("newTask.createAsTui")}</Button> : null}
		</div>
	) : null;
	return header ? <>{feedback}{orchestratorButton}{newTaskButton}</> : <>
		<div className="mt-5 flex items-center gap-2">{orchestratorButton}{newTaskButton}</div>
		{feedback}
	</>;
}
