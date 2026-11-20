import { useId } from "react";
import { useTranslation } from "react-i18next";
import type { ScriptRun } from "../lib/cloud-script-runs";
import { dragStyle } from "./PageTopbar";
import { cn } from "../lib/utils";
import { humanBubbleClass } from "./chat/ChatTimelineItems";
import { CoderWorkspaceLinkButton } from "./CoderWorkspaceLinkButton";
import { ScriptRunPanel } from "./ScriptRunPanel";
import { MultiStepLoader } from "./ui/multi-step-loader";

export function CloudSessionStartupLoader({
	activeIndex = 0,
	completed = false,
	startupNote,
	coderWorkspaceUrl,
	setupRun,
	queuedMessage,
}: {
	activeIndex?: number;
	completed?: boolean;
	startupNote?: string;
	/** Bring-your-own Coder workspace page, linked beside the workspace step. */
	coderWorkspaceUrl?: string;
	/** The project's Setup run while it runs or after it failed, shown under the steps. */
	setupRun?: ScriptRun;
	/**
	 * The person's first message to a Chat session this screen covers: the chat
	 * would show it at once, and it waits for the workspace, so it shows here.
	 */
	queuedMessage?: string;
}) {
	const { t } = useTranslation();
	const queuedMessageLabel = useId();
	const panel = setupRun && !completed ? <ScriptRunPanel className="w-full" run={setupRun} /> : null;
	// Kept through the completion fade, so nothing jumps as the screen leaves.
	const message = queuedMessage ? (
		<figure aria-labelledby={queuedMessageLabel} className="flex min-w-0 flex-col items-center gap-1.5">
			<figcaption id={queuedMessageLabel} className="text-xs text-muted-foreground">{t("terminal.sessionLoader.queuedMessage")}</figcaption>
			{/* The chat's queued message: the agent has not seen it yet. */}
			<p data-queued="" className={cn(humanBubbleClass(true), "max-h-40 overflow-y-auto break-words whitespace-pre-wrap text-pretty")}>{queuedMessage}</p>
		</figure>
	) : null;
	const wide = Boolean(panel || message);
	const loader = <MultiStepLoader
		ariaLabel={t("terminal.sessionLoader.label")}
		activeIndex={completed ? 3 : activeIndex}
		className={wide ? "self-center" : startupNote ? "w-full max-w-none" : undefined}
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
			className={cn("absolute inset-0 z-chrome grid place-items-center bg-background", wide && "overflow-y-auto", completed && "cloud-session-loader--complete pointer-events-none")}
			data-testid="cloud-session-loader-screen"
			data-startup-cover=""
		>
			{/* The loader covers the session header; on macOS keep its edge draggable. */}
			{dragStyle ? <div aria-hidden="true" className="absolute inset-x-0 top-0 h-toolbar" style={dragStyle} /> : null}
			{startupNote || wide ? (
				<div className={cn("flex max-w-[calc(100%-2rem)] flex-col gap-4", wide ? "w-[36rem] py-6" : "w-80")}>
					{message}
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
