import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { isActiveInstallJob, isWaitingForIdleJob, type InstallJob } from "../../../hooks/useHarnessUpdates";
import { cn } from "../../../lib/utils";
import { Button } from "../../ui/button";

/** How long a running install or update goes before it explains the wait. */
export const SLOW_JOB_HINT_MS = 60_000;

/** Elapsed time as a clock reads it: 0:07, 2:41, 1:02:03. */
export function formatElapsed(ms: number): string {
	const total = Math.max(0, Math.floor(ms / 1000));
	const hours = Math.floor(total / 3600);
	const minutes = Math.floor((total % 3600) / 60);
	const seconds = String(total % 60).padStart(2, "0");
	return hours > 0 ? `${hours}:${String(minutes).padStart(2, "0")}:${seconds}` : `${minutes}:${seconds}`;
}

// ANSI CSI and OSC sequences that colour or retitle terminal output.
// eslint-disable-next-line no-control-regex
const TERMINAL_ESCAPE = /\x1b\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)/g;

/** Installer output as a terminal would show it: escapes removed, and a line
 * redrawn with carriage returns (a progress bar) reduced to its last draw. */
export function terminalText(output: string): string {
	return output.replace(TERMINAL_ESCAPE, "").replace(/\r\n/g, "\n").split("\n")
		.map((line) => line.split("\r").filter((segment) => segment.trim() !== "").at(-1) ?? "")
		.join("\n").trimEnd();
}

type TimedJob = Pick<InstallJob, "status" | "startedAt" | "runStartedAt">;

/** How long a running job's command has run, ticking each second; undefined
 * while it is not running. An update that first waited for idle sessions is
 * timed from when its command started, not from when it began waiting. */
export function useJobElapsed(job?: TimedJob): number | undefined {
	const running = isActiveInstallJob(job) && !isWaitingForIdleJob(job);
	const begun = job?.runStartedAt ?? job?.startedAt;
	const startedAt = running && begun ? Date.parse(begun) : NaN;
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		if (Number.isNaN(startedAt)) return;
		setNow(Date.now());
		const timer = window.setInterval(() => setNow(Date.now()), 1000);
		return () => window.clearInterval(timer);
	}, [startedAt]);
	return Number.isNaN(startedAt) ? undefined : Math.max(0, now - startedAt);
}

/** How long a running job has run, beside its busy label. Hidden from
 * assistive technology so a status region does not announce every second. */
export function JobElapsed({ job, className }: { job?: TimedJob; className?: string }) {
	const elapsedMs = useJobElapsed(job);
	if (elapsedMs === undefined) return null;
	return <span aria-hidden="true" data-job-elapsed="" className={cn("tabular-nums", className)}>{formatElapsed(elapsedMs)}</span>;
}

/** How long a running job has run, on its own line, for a surface whose busy
 * button keeps one width. Hidden from assistive technology like JobElapsed. */
export function JobElapsedLine({ job, className }: { job?: TimedJob; className?: string }) {
	const { t } = useTranslation();
	const elapsedMs = useJobElapsed(job);
	if (elapsedMs === undefined) return null;
	return <p aria-hidden="true" data-job-elapsed="" className={cn("tabular-nums", className)}>{t("settings.harness.elapsed", { elapsed: formatElapsed(elapsedMs) })}</p>;
}

/**
 * Once a job has run a while, says why it can take minutes and offers the
 * output the installer has printed so far, which refreshes as the job polls.
 */
export function HarnessJobSlowDetail({ job, className }: { job?: InstallJob; className?: string }) {
	const { t } = useTranslation();
	const elapsedMs = useJobElapsed(job);
	const outputId = useId();
	const [open, setOpen] = useState(false);
	const outputRef = useRef<HTMLPreElement>(null);
	const pinnedToEnd = useRef(true);
	const output = job?.output ? terminalText(job.output) : "";
	// Follow new output like a terminal unless the reader scrolled up.
	useLayoutEffect(() => {
		const element = outputRef.current;
		if (element && pinnedToEnd.current) element.scrollTop = element.scrollHeight;
	}, [open, output]);
	if (!job || elapsedMs === undefined || elapsedMs < SLOW_JOB_HINT_MS) return null;
	const downloading = job.status === "running" || job.status === "installing";
	return <div className={cn("flex flex-col items-start gap-1 text-caption text-settings-muted", className)}>
		{downloading ? <p role="status" className="break-words">{t("settings.harness.slowDownloadHint")}</p> : null}
		<Button type="button" size="sm" variant="ghost" className="-ml-2 h-6 px-2 text-[length:var(--font-size-caption)]" aria-expanded={open} aria-controls={outputId}
			onClick={() => {
				// Reopened output starts at the latest line again.
				pinnedToEnd.current = true;
				setOpen((current) => !current);
			}}>
			{t(open ? "settings.harness.hideOutput" : "settings.harness.showOutput")}
		</Button>
		{open ? <pre id={outputId} ref={outputRef} aria-label={t("settings.harness.installOutput")} tabIndex={0}
			onScroll={(event) => {
				const element = event.currentTarget;
				pinnedToEnd.current = element.scrollHeight - element.scrollTop - element.clientHeight < 8;
			}}
			className="settings-thin-scrollbar max-h-40 w-full overflow-auto overscroll-contain whitespace-pre-wrap break-words rounded-md border border-(--color-border-settings-input) bg-(--color-bg-settings-input) p-2 font-mono text-settings-label focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
			{output || t("settings.harness.noOutputYet")}
		</pre> : null}
	</div>;
}
