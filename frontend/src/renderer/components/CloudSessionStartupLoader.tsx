import { useTranslation } from "react-i18next";
import type { ScriptRun } from "../lib/cloud-script-runs";
import { cn } from "../lib/utils";
import { CoderWorkspaceLinkButton } from "./CoderWorkspaceLinkButton";
import { ScriptRunPanel } from "./ScriptRunPanel";
import { MultiStepLoader } from "./ui/multi-step-loader";

export function CloudSessionStartupLoader({
	activeIndex = 0,
	completed = false,
	startupNote,
	coderWorkspaceUrl,
	setupRun,
}: {
	activeIndex?: number;
	completed?: boolean;
	startupNote?: string;
	/** Bring-your-own Coder workspace page, linked beside the workspace step. */
	coderWorkspaceUrl?: string;
	/** The project's Setup run while it runs or after it failed, shown under the steps. */
	setupRun?: ScriptRun;
}) {
	const { t } = useTranslation();
	const panel = setupRun && !completed ? <ScriptRunPanel className="w-full" run={setupRun} /> : null;
	const loader = <MultiStepLoader
		ariaLabel={t("terminal.sessionLoader.label")}
		activeIndex={completed ? 3 : activeIndex}
		className={panel ? "self-center" : startupNote ? "w-full max-w-none" : undefined}
		complete={completed}
		steps={[
			t("terminal.sessionLoader.workspace"),
			t("terminal.sessionLoader.worker"),
			t("terminal.sessionLoader.repositoryAgent"),
			t("terminal.sessionLoader.terminal"),
		]}
		stepAccessories={coderWorkspaceUrl ? [<CoderWorkspaceLinkButton key="workspace" url={coderWorkspaceUrl} />] : undefined}
	/>;
	return (
		<div
			// Cover session chrome while staying below app dialogs and menus.
			// A long Setup log scrolls the screen rather than clipping the steps.
			className={cn("absolute inset-0 z-chrome grid place-items-center bg-background", panel && "overflow-y-auto", completed && "cloud-session-loader--complete pointer-events-none")}
			data-testid="cloud-session-loader-screen"
		>
			{startupNote || panel ? (
				<div className={cn("flex max-w-[calc(100%-2rem)] flex-col gap-4", panel ? "w-[36rem] py-6" : "w-80")}>
					{loader}
					{startupNote ? (
						<p className="text-xs leading-relaxed text-muted-foreground" data-testid="cloud-session-startup-note">
							{startupNote} {t("cloud.startupError.stillRetrying")}
						</p>
					) : null}
					{panel}
				</div>
			) : loader}
		</div>
	);
}
