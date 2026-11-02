import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { isActiveInstallJob, type InstallJob } from "../../../hooks/useHarnessUpdates";
import { cn } from "../../../lib/utils";
import { installPercent, type InstallStage } from "./describe-harness";
import { HARNESS_CARD_ROW_CLASS } from "./harness-ui";

/** How long the full bar stays up after an install this page watched succeeds. */
export const INSTALL_DONE_VISIBLE_MS = 4_000;

/** True for a few seconds after a job this page saw running succeeds, so the
 * bar finishes full instead of vanishing. A job that had already finished
 * when the page opened shows nothing. */
export function useInstallJustFinished(job?: Pick<InstallJob, "status" | "startedAt">): boolean {
	const watched = useRef<string | undefined>(undefined);
	const [doneFor, setDoneFor] = useState<string | undefined>(undefined);
	const key = job?.startedAt ?? undefined;
	const status = job?.status;
	useEffect(() => {
		if (isActiveInstallJob(status ? { status } : undefined)) {
			watched.current = key;
			return;
		}
		if (status !== "succeeded" || !key || watched.current !== key) return;
		watched.current = undefined;
		setDoneFor(key);
		const timer = window.setTimeout(() => setDoneFor(undefined), INSTALL_DONE_VISIBLE_MS);
		return () => window.clearTimeout(timer);
	}, [status, key]);
	return Boolean(doneFor && doneFor === key && job?.status === "succeeded");
}

/** Install or update progress: a bar that fills to the installer's reported
 * percentage (or sweeps when it states none) and the installer's latest line.
 * The stage and percentage are named on the action button, not repeated here. */
export function HarnessInstallProgress({ job, stage, label }: { job: InstallJob; stage?: InstallStage; label: string }) {
	const { t } = useTranslation();
	const done = stage === "done";
	const percent = installPercent(job);
	const detail = done ? t("settings.harness.progress.done") : stage === "verifying" ? t("settings.harness.progress.verifyingDetail") : job.progress?.detail;
	return <div data-harness-install-progress={stage ?? "starting"} className={cn(HARNESS_CARD_ROW_CLASS, "flex-col items-stretch gap-2.5")}>
		{/* Keyed apart so the fill starts from its value, not from the sweep's width. */}
		{done || percent !== undefined ? <div
			key="determinate"
			className="ao-install-progress"
			role="progressbar"
			aria-label={label}
			aria-valuemin={0}
			aria-valuemax={100}
			aria-valuenow={done ? 100 : percent}
		>
			<div className="ao-install-progress__bar ao-install-progress__bar--determinate" style={{ width: `${done ? 100 : percent}%` }} />
		</div> : <div key="indeterminate" className="ao-install-progress" role="progressbar" aria-label={label}>
			<div className="ao-install-progress__bar" />
		</div>}
		{detail ? <p className="truncate font-mono text-xs text-settings-muted" title={detail}>{detail}</p> : null}
	</div>;
}
