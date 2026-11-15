import { Check, X } from "@/components/app-icons";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import type { SessionProvisionStep } from "../../types/workspace";
import { Button } from "../ui/button";
import { ResponseSpinner, STEP_MARK_SLOT_CLASS } from "./ChatTimelineItems";

type StepStatus = SessionProvisionStep["status"] | "failed";

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
	const [stagger] = useState(() => !failed && !steps.some((step) => step.status === "done") &&
		steps.every((step) => !step.startedAt || Date.now() - Date.parse(step.startedAt) < 3000));
	return (
		<section aria-label={t("chat.startup.label")} data-testid="session-startup" data-failed={failed || undefined}>
			<ol aria-label={t("chat.startup.steps")} className="mt-1 flex flex-col">
				{steps.map((step, index) => {
					const status: StepStatus = failed && step.status === "running" ? "failed" : step.status;
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
							className={cn("flex min-h-7 items-center gap-2 text-xs", stagger && "session-startup-step-in")}
							style={stagger ? { animationDelay: `${index * 90}ms` } : undefined}
						>
							<span className={STEP_MARK_SLOT_CLASS}>
								<StepIcon status={status} />
							</span>
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
				<div className="mt-1 flex flex-col items-start gap-2 pl-5">
					{error ? <p className="text-xs leading-snug text-muted-foreground">{error}</p> : null}
					<p className="text-xs leading-snug text-muted-foreground">{t("chat.startup.saved")}</p>
					{onRetry ? (
						<Button type="button" size="sm" variant="secondary" onClick={onRetry} disabled={retrying}>
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
			return (
				<svg aria-hidden="true" viewBox="0 0 16 16" className={cn(className, "text-passive")}>
					<circle cx="8" cy="8" r="6.5" fill="none" stroke="currentColor" strokeWidth="1.5" strokeDasharray="2.6 2.7" strokeLinecap="round" />
				</svg>
			);
	}
}



