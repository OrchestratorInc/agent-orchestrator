import type { ReactElement } from "react";
import { useTranslation } from "react-i18next";
import type { WorkspaceSession } from "../types/workspace";
import type { PausePolicy } from "../hooks/useAgentPause";
import { Popover, PopoverContent, PopoverTrigger } from "./ui/popover";

/**
 * Asked only while the agent is mid-turn: pausing then either waits for the
 * turn to finish (drain) or sends Ctrl-C now (interrupt). An idle agent
 * pauses without this step.
 */
export function AgentPausePopover({
	blocked,
	onChoose,
	onOpenChange,
	open,
	session,
	trigger,
}: {
	/** The previous drained pause could not finish; only interrupt is left. */
	blocked?: boolean;
	onChoose: (policy: PausePolicy) => void;
	onOpenChange: (open: boolean) => void;
	open: boolean;
	session: WorkspaceSession;
	trigger: ReactElement;
}) {
	const { t } = useTranslation();
	return (
		<Popover onOpenChange={onOpenChange} open={open}>
			<PopoverTrigger asChild>{trigger}</PopoverTrigger>
			<PopoverContent
				align="end"
				aria-label={t("pause.dialogNamed", { title: session.title })}
				className="w-64 max-w-[calc(100vw-1rem)] p-3 shadow-lg"
				collisionPadding={8}
				onClick={(event) => event.stopPropagation()}
				role="dialog"
				side="bottom"
				sideOffset={6}
			>
				<p className="text-control font-semibold text-foreground">{t("pause.dialog")}</p>
				<p className="mt-1 text-caption leading-4 text-muted-foreground">
					{blocked ? t("pause.bodyBlocked") : t("pause.body")}
				</p>
				<div className="mt-3 flex justify-end gap-1.5">
					<button
						className="h-control-md rounded-md px-2.5 text-xs font-medium text-muted-foreground transition-colors hover:bg-interactive-hover hover:text-foreground"
						onClick={() => onChoose("interrupt")}
						type="button"
					>
						{t("pause.now")}
					</button>
					{blocked ? null : (
						<button
							className="h-control-md rounded-md bg-accent-strong px-2.5 text-xs font-semibold text-accent-foreground transition-[filter] hover:brightness-110"
							onClick={() => onChoose("drain")}
							type="button"
						>
							{t("pause.afterTurn")}
						</button>
					)}
				</div>
			</PopoverContent>
		</Popover>
	);
}
