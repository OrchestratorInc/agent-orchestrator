import { Check, Circle, X } from "lucide-react";
import { useEffect, useState } from "react";
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
	branch,
	onRetry,
	retrying,
	retryError,
}: {
	failed: boolean;
	steps: readonly SessionProvisionStep[];
	error?: string;
	agentName: string;
	branch?: string;
	onRetry?: () => void;
	retrying?: boolean;
	retryError?: string;
}) {
	const { t } = useTranslation();
	const now = useNowWhile(!failed && steps.some((step) => step.status === "running"));
	return (
		<section aria-label={t("chat.startup.label")} data-testid="session-startup" data-failed={failed || undefined}>
			<ol aria-label={t("chat.startup.steps")} className="mt-1 flex flex-col">
				{steps.map((step) => {
					// The daemon leaves the stage it stopped in "running"; the failed
					// session is what makes that stage the one that failed.
					const status: StepStatus = failed && step.status === "running" ? "failed" : step.status;
					const elapsed = status === "failed" ? undefined : stepElapsedMs(step, now);
					const detail = step.id === "worktree" ? branch : undefined;
					return (
						<li
							key={step.id}
							data-step={step.id}
							data-status={status}
							className="flex min-h-7 items-center gap-1.5 text-xs"
						>
							<StepIcon status={status} />
							<span className="sr-only">{t(`chat.startup.status.${status}`)}:</span>
							<span
								className={cn(
									"shrink-0",
									status === "pending" && "text-passive",
									status === "running" && "text-foreground",
									status === "done" && "text-muted-foreground",
									status === "failed" && "text-destructive",
								)}
							>
								{step.id === "agent"
									? t("chat.startup.step.agent", { agent: agentName })
									: t(`chat.startup.step.${step.id}`)}
							</span>
							{detail && status !== "pending" ? (
								<span className="min-w-0 truncate font-mono text-caption text-passive" title={detail}>
									{detail}
								</span>
							) : null}
							{elapsed !== undefined ? (
								<span className="ml-auto shrink-0 pl-2 font-mono text-caption tabular-nums text-passive">
									{formatStepDuration(elapsed, status === "running")}
								</span>
							) : null}
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

function stepElapsedMs(step: SessionProvisionStep, now: number): number | undefined {
	if (!step.startedAt) return undefined;
	const start = Date.parse(step.startedAt);
	const end = step.endedAt ? Date.parse(step.endedAt) : now;
	if (!Number.isFinite(start) || !Number.isFinite(end)) return undefined;
	return Math.max(0, end - start);
}

/** A finished step keeps a tenth of a second; a running one ticks whole seconds. */
export function formatStepDuration(ms: number, running = false): string {
	const seconds = ms / 1000;
	if (seconds < 10 && !running) return `${seconds.toFixed(1)}s`;
	if (seconds < 60) return `${Math.floor(seconds)}s`;
	return `${Math.floor(seconds / 60)}m ${Math.floor(seconds % 60)}s`;
}

/** Ticks once a second while a step runs, so its elapsed label stays live. */
function useNowWhile(active: boolean): number {
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		if (!active) return;
		setNow(Date.now());
		const timer = setInterval(() => setNow(Date.now()), 1000);
		return () => clearInterval(timer);
	}, [active]);
	return now;
}
