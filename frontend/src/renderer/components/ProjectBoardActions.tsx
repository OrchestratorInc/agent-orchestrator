import { type CSSProperties } from "react";
import { useTranslation } from "react-i18next";
import { Plus } from "@/components/app-icons";
import type { ProjectOrchestratorAction } from "../hooks/useProjectOrchestratorAction";
import { useSimplifiedTopbar } from "../hooks/useSimplifiedTopbar";
import { getAgentActivityView } from "../lib/session-presentation";
import { useOrchestratorReplyState } from "../stores/orchestrator-reply-store";
import { TopbarActionError, TopbarButton } from "./TopbarButton";
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
	const simplifiedTopbar = useSimplifiedTopbar();
	const { projectId, hostId, orchestrator, isSpawning, isProjectRestarting, isProvisioning, spawnError, canCreateAsTui,
		openNewTask, openOrchestrator } = actions;
	const header = placement === "header";
	const orchestratorReply = useOrchestratorReplyState({ projectId, hostId, orchestrator, viewing: false });
	const busy = isSpawning || isProjectRestarting || isProvisioning;
	const activity = orchestrator ? getAgentActivityView(orchestrator.activity, t).label : undefined;
	const actionLabel = orchestrator ? t("shell.openOrchestrator") : t("shell.spawnOrchestrator");
	const busyLabel = isProjectRestarting ? t("shell.restartingDots") : isProvisioning
		? t("shell.provisioningDots", { defaultValue: "Setting up…" }) : isSpawning ? t("shell.spawningDots") : undefined;
	// The simplified bar shows Orchestrator quiet, labeled, first; the previous
	// header never demotes it. The empty board's body keeps it as the call to action.
	const orchestratorVariant = simplifiedTopbar
		? (header ? "action" : quiet ? "secondary" : "primary")
		: (header || !quiet ? "primary" : "secondary");
	const orchestratorButton = (
		<Tooltip>
			<TooltipTrigger asChild>
				<span className="inline-flex" style={style}>
					<TopbarButton
						aria-label={orchestratorReplyLabel(activity ? t("shell.orchestratorWithActivity", { activity }) : actionLabel, activity && orchestratorReply === "working" ? null : orchestratorReply, t)}
						aria-busy={busy}
						className={header ? "topbar-control--labeled" : undefined}
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
					</TopbarButton>
				</span>
			</TooltipTrigger>
			<TooltipContent side="bottom">{busyLabel ?? orchestratorReplyLabel(actionLabel, orchestratorReply, t)}</TooltipContent>
		</Tooltip>
	);
	const newTaskButton = (
		<Tooltip>
			<TooltipTrigger asChild>
				<span className="inline-flex" style={style}>
					<TopbarButton
						aria-label={t("shell.newTask")}
						className={header ? "topbar-control--labeled" : undefined}
						data-priority={header ? "primary" : undefined}
						disabled={isProjectRestarting || isProvisioning}
						onClick={() => {
							if (header) captureTopbarAction("new_task", "board");
							openNewTask();
						}}
						// The simplified bar's one filled action is New task; the previous header
						// keeps it a utility control (one filled primary per region).
						variant={header ? (simplifiedTopbar ? "primary" : "action") : quiet || cloud ? "secondary" : "accent"}
					>
						<Plus className="size-icon-md" aria-hidden="true" />
						<span data-compact-label={header ? "" : undefined}>{t(header ? "newTask.task" : "shell.newTask")}</span>
					</TopbarButton>
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
			{canCreateAsTui ? <TopbarButton disabled={busy} onClick={() => openOrchestrator("tui")} style={style} variant="primary">{t("newTask.createAsTui")}</TopbarButton> : null}
		</div>
	) : null;
	return header ? (simplifiedTopbar ? <>{feedback}{orchestratorButton}{newTaskButton}</> : <>{feedback}{newTaskButton}{orchestratorButton}</>) : <>
		<div className="mt-5 flex items-center gap-2">{orchestratorButton}{newTaskButton}</div>
		{feedback}
	</>;
}
