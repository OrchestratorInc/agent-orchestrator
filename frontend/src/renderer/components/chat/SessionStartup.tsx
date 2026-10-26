import { Check, Circle, X } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import type { SessionProvisionStep } from "../../types/workspace";
import { Button } from "../ui/button";
import { ResponseSpinner } from "./ChatTimelineItems";

type StepStatus = SessionProvisionStep["status"] | "failed";

/**
 * The checklist a starting Chat session shows under its opening brief. The
 * shared LiveResponseStatus header stays mounted while this checklist collapses
 * on handoff. A failed start keeps its failed step and Retry.
 */
export function SessionStartup({
	failed,
	steps,
	error,
	agentName,
	onRetry,
	retrying,
	retryError,
}: {
	failed: boolean;
	steps: readonly SessionProvisionStep[];
	error?: string;
	agentName: string;
	branch?: string;
	namingBranch?: boolean;
	onRetry?: () => void;
	retrying?: boolean;
	retryError?: string;
}) {
	const { t } = useTranslation();
	// A start that has just begun reveals its steps one by one; reopening a session
	// part-way through setup shows them at once.
	const [stagger] = useState(() => !failed && !steps.some((step) => step.status === "done") &&
		steps.every((step) => !step.startedAt || Date.now() - Date.parse(step.startedAt) < 3000));
	return (
		<section aria-label={t("chat.startup.label")} data-testid="session-startup" data-failed={failed || undefined}>
			<ol aria-label={t("chat.startup.steps")} className="mt-1 flex flex-col">
				{steps.map((step, index) => {
					// The daemon leaves the stage it stopped in "running"; the failed
					// session is what makes that stage the one that failed.
					const status: StepStatus = failed && step.status === "running" ? "failed" : step.status;
					// One plain-language label per step; the workspace step reports when it is done.
					const label = step.id === "agent"
						? t("chat.startup.step.agent", { agent: agentName })
						: step.id === "worktree" && status === "done"
							? t("chat.startup.step.worktreeDone")
							: t(`chat.startup.step.${step.id}`);
					return (
						<li
							key={step.id}
							data-step={step.id}
							data-status={status}
							className={cn("flex min-h-7 items-center gap-1.5 text-xs", stagger && "session-startup-step-in")}
							style={stagger ? { animationDelay: `${index * 90}ms` } : undefined}
						>
							<StepIcon status={status} />
							<span className="sr-only">{t(`chat.startup.status.${status}`)}:</span>
							<span
								className={cn(
									"min-w-0 truncate",
									status === "pending" && "text-passive",
									status === "running" && "chat-working-shimmer",
									status === "done" && "text-muted-foreground",
									status === "failed" && "text-destructive",
								)}
							>
								{label}
							</span>
						</li>
					);
				})}
			</ol>
			{failed ? (
				<div className="mt-1 flex flex-col items-start gap-2 pl-[18px]">
					{error ? <p className="text-xs leading-snug text-muted-foreground">{error}</p> : null}
					<p className="text-xs leading-snug text-muted-foreground">{t("chat.startup.saved")}</p>
					{onRetry ? (
						<Button type="button" size="sm" variant="outline" onClick={onRetry} disabled={retrying}>
							{retrying ? t("chat.startup.retrying") : t("chat.startup.retry")}
						</Button>
					) : null}
					{retryError ? <p role="alert" className="text-xs leading-snug text-destructive">{retryError}</p> : null}
				</div>
			) : null}
		</section>
	);
}

function StepIcon({ status }: { status: StepStatus }) {
	const className = "size-3 shrink-0";
	switch (status) {
		case "running":
			return <ResponseSpinner />;
		case "done":
			return <Check aria-hidden="true" className={cn(className, "text-muted-foreground")} />;
		case "failed":
			return <X aria-hidden="true" className={cn(className, "text-destructive")} />;
		case "pending":
			return <Circle aria-hidden="true" className={cn(className, "text-passive")} />;
	}
}



