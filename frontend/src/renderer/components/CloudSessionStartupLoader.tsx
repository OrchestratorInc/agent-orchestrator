import { useEffect, useId, useState } from "react";
import { useTranslation } from "react-i18next";
import type { CloudCpSessionReadyCheck } from "../lib/cloud-cp";
import { desktopClockLeadMs, formatScriptDuration, type ScriptRun } from "../lib/cloud-script-runs";
import { dragStyle } from "./PageTopbar";
import { cn } from "../lib/utils";
import { humanBubbleClass } from "./chat/ChatTimelineItems";
import { CoderWorkspaceLinkButton } from "./CoderWorkspaceLinkButton";
import { ScriptRunPanel } from "./ScriptRunPanel";
import { MultiStepLoader } from "./ui/multi-step-loader";

/** The time now, ticking once a second while mounted. */
function useSecondTicks(): number {
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		const timer = window.setInterval(() => setNow(Date.now()), 1_000);
		return () => window.clearInterval(timer);
	}, []);
	return now;
}

/**
 * Under the workspace step while AO waits for a bring-your-own workspace's
 * own ready check: how long since Coder reported the workspace ready (live,
 * against the control plane's clock), how many checks have run, and the
 * latest check's output, which the control plane bounds and redacts. A check
 * that prints nothing (a bare grep) shows no output block.
 */
function ReadyCheckWait({ check }: { check: CloudCpSessionReadyCheck }) {
	const { t } = useTranslation();
	const outputId = useId();
	const now = useSecondTicks();
	const since = Date.parse(check.since);
	const elapsedMs = Number.isFinite(since) ? now - desktopClockLeadMs() - since : 0;
	// AO stops checking at the limit, so the time shown never runs past it.
	const elapsed = formatScriptDuration(check.timeoutSeconds ? Math.min(elapsedMs, check.timeoutSeconds * 1_000) : elapsedMs);
	const progress = check.timeoutSeconds
		? t("terminal.sessionLoader.readyCheck.progressWithLimit", { elapsed, limit: formatScriptDuration(check.timeoutSeconds * 1_000), count: check.attempts })
		: t("terminal.sessionLoader.readyCheck.progress", { elapsed, count: check.attempts });
	const output = check.output?.trim();
	return (
		<section aria-label={t("terminal.sessionLoader.readyCheck.step")} className="flex min-w-0 flex-col gap-2" data-testid="cloud-session-ready-check">
			<p className="text-xs leading-relaxed tabular-nums text-muted-foreground" data-testid="cloud-session-ready-check-progress">{progress}</p>
			{output ? (
				<div className="flex min-w-0 flex-col gap-1">
					<p className="text-caption text-muted-foreground" id={outputId}>{t("terminal.sessionLoader.readyCheck.output", { code: check.exitCode })}</p>
					<pre
						aria-labelledby={outputId}
						className="scrollbar-none max-h-40 cursor-text select-text overflow-auto whitespace-pre-wrap break-words border border-border bg-background px-2.5 py-2 font-mono text-caption leading-relaxed text-muted-foreground"
						data-testid="cloud-session-ready-check-output"
						tabIndex={0}
					>
						{output}
					</pre>
				</div>
			) : null}
		</section>
	);
}

export function CloudSessionStartupLoader({
	activeIndex = 0,
	completed = false,
	startupNote,
	readyCheck,
	coderWorkspaceUrl,
	setupRun,
	queuedMessage,
}: {
	activeIndex?: number;
	completed?: boolean;
	startupNote?: string;
	/**
	 * A bring-your-own workspace's own ready check AO is waiting for; shown
	 * under the steps in place of the startup note.
	 */
	readyCheck?: CloudCpSessionReadyCheck;
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
	const readyCheckWait = readyCheck && !completed ? <ReadyCheckWait check={readyCheck} /> : null;
	const wide = Boolean(panel || message || readyCheckWait);
	const loader = <MultiStepLoader
		ariaLabel={t("terminal.sessionLoader.label")}
		activeIndex={completed ? 3 : activeIndex}
		className={wide ? "self-center" : startupNote ? "w-full max-w-none" : undefined}
		complete={completed}
		steps={[
			// Coder already reports the workspace ready; AO waits on its own setup.
			readyCheckWait && activeIndex === 0 ? t("terminal.sessionLoader.readyCheck.step") : t("terminal.sessionLoader.workspace"),
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
					{readyCheckWait ?? (startupNote ? (
						<p className="text-xs leading-relaxed text-muted-foreground" data-testid="cloud-session-startup-note">
							{startupNote} {t("cloud.startupError.stillRetrying")}
						</p>
					) : null)}
					{panel}
				</div>
			) : loader}
		</div>
	);
}
