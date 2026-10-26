import { Check, ChevronRight, Circle, Clock, Loader2, X } from "lucide-react";
import { type CSSProperties, useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { useHarnessUpdateNoticeClearance } from "../hooks/useHarnessUpdateNoticeClearance";
import { stripAnsi } from "../lib/ansi";
import {
	desktopClockLeadMs,
	formatScriptDuration,
	noteControlPlaneEventArrival,
	type ScriptRun,
	type ScriptRunStep,
} from "../lib/cloud-script-runs";
import { cn } from "../lib/utils";
import { CopyButton } from "./chat/CopyButton";

/**
 * The header and step rows' right padding (px-2.5) plus whatever the panel
 * needs to keep its timers clear of the harness update notice in the corner.
 */
const ROW_PAD_RIGHT = "pr-[calc(0.625rem+var(--script-run-notice-clearance,0px))]";

/** Ticks once a second while `active`, for live step and run timers. */
function useNow(active: boolean): number {
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		if (!active) return;
		setNow(Date.now());
		const timer = window.setInterval(() => setNow(Date.now()), 1_000);
		return () => window.clearInterval(timer);
	}, [active]);
	return now;
}

function elapsedSince(startedAt: string | undefined, now: number): number | undefined {
	if (!startedAt) return undefined;
	const start = Date.parse(startedAt);
	return Number.isFinite(start) ? Math.max(0, now - start) : undefined;
}

/**
 * A step is shown as running only while its run is: an interrupted run's step
 * stopped, and a run that ended without reporting it has no known result.
 */
function displayStatus(step: ScriptRunStep, run: ScriptRun): ScriptRunStep["status"] | "stopped" | "unknown" {
	if (step.status !== "running") return step.status;
	if (run.status === "interrupted") return "stopped";
	if (run.status === "unknown") return "unknown";
	return step.status;
}

function StepIcon({ status }: { status: ReturnType<typeof displayStatus> }) {
	if (status === "running") return <Loader2 aria-hidden="true" className="size-3.5 shrink-0 animate-spin text-status-working" />;
	if (status === "succeeded") return <Check aria-hidden="true" className="size-3.5 shrink-0 text-success" />;
	if (status === "failed" || status === "timed_out") return <X aria-hidden="true" className="size-3.5 shrink-0 text-error" />;
	return <Circle aria-hidden="true" className="size-3 shrink-0 text-passive" />;
}

/**
 * The step's output tail: selectable monospace that follows the bottom while
 * the step runs, until the reader scrolls up to read something.
 */
function StepOutput({ step, running, label }: { step: ScriptRunStep; running: boolean; label: string }) {
	const { t } = useTranslation();
	const pre = useRef<HTMLPreElement>(null);
	const follow = useRef(true);
	const output = step.output ? stripAnsi(step.output) : "";
	useLayoutEffect(() => {
		const node = pre.current;
		if (node && follow.current) node.scrollTop = node.scrollHeight;
	}, [output]);
	if (step.outputHidden) {
		return <p className="px-1 text-caption text-muted-foreground" data-testid="script-step-output-hidden">{t("scriptRun.outputHidden")}</p>;
	}
	if (!output) {
		return <p className="px-1 text-caption text-muted-foreground">{running ? t("scriptRun.waitingForOutput") : t("scriptRun.noOutput")}</p>;
	}
	return (
		<div className="relative">
			<pre
				aria-label={label}
				className="scrollbar-none max-h-60 cursor-text select-text overflow-auto whitespace-pre-wrap break-words border border-border bg-background py-2 pl-2.5 pr-9 font-mono text-caption leading-relaxed text-muted-foreground"
				data-testid="script-step-output"
				onScroll={(event) => {
					const node = event.currentTarget;
					// Within a line of the bottom counts as following along; further up
					// is a deliberate scroll and is left alone until the reader returns.
					follow.current = node.scrollHeight - node.scrollTop - node.clientHeight < 24;
				}}
				ref={pre}
				tabIndex={0}
			>
				{output}
			</pre>
			<CopyButton
				className="absolute right-1 top-1 size-6 bg-background"
				compact
				copiedLabel={t("scriptRun.copied")}
				label={t("scriptRun.copyOutput")}
				text={output}
			/>
		</div>
	);
}

/**
 * What changed in a run since its last render, for screen readers: a step
 * starting, succeeding or failing. The output itself is never announced; it
 * changes about once a second while a step runs. Nothing is said for the state
 * the panel first draws, only for transitions it sees happen.
 */
function useStepAnnouncement(run: ScriptRun, title: string): string {
	const { t } = useTranslation();
	const previous = useRef<{ runId: string; statuses: Map<number, ScriptRunStep["status"]> }>(undefined);
	const [message, setMessage] = useState("");
	useEffect(() => {
		const before = previous.current;
		previous.current = { runId: run.runId, statuses: new Map(run.steps.map((step) => [step.step, step.status])) };
		if (!before) return;
		const sameRun = before.runId === run.runId;
		const lines: string[] = [];
		for (const step of run.steps) {
			if ((sameRun ? before.statuses.get(step.step) : "pending") === step.status) continue;
			const values = { phase: title, step: step.step, total: run.total, command: step.command || t("scriptRun.stepNumber", { step: step.step }) };
			if (step.status === "running") lines.push(t("scriptRun.announce.started", values));
			else if (step.status === "succeeded") lines.push(t("scriptRun.announce.succeeded", values));
			else if (step.status === "failed") lines.push(t("scriptRun.announce.failed", values));
			else if (step.status === "timed_out") lines.push(t("scriptRun.announce.timedOut", values));
		}
		if (lines.length > 0) setMessage(lines.join(" "));
	}, [run, t, title]);
	return message;
}

function StepNotes({ step }: { step: ScriptRunStep }) {
	const { t } = useTranslation();
	const failed = step.status === "failed" || step.status === "timed_out";
	const lines: string[] = [];
	if (step.status === "timed_out") lines.push(step.durationMs !== undefined ? t("scriptRun.timedOutAfter", { duration: formatScriptDuration(step.durationMs) }) : t("scriptRun.stepStatus.timedOut"));
	else if (failed && step.exitCode !== undefined && step.exitCode >= 0) lines.push(t("scriptRun.exitCode", { code: step.exitCode }));
	if (failed && step.user) {
		lines.push(step.groups?.length
			? t("chat.projectSetup.ranAsWithGroups", { user: step.user, groups: step.groups.join(" ") })
			: t("chat.projectSetup.ranAs", { user: step.user }));
	}
	if (failed && step.dir) lines.push(t("chat.projectSetup.ranIn", { dir: step.dir }));
	// An old Cleanup hook marks steps it kept no output for as truncated: there
	// is no tail to call the end of, only the run's full log.
	const hasOutput = step.output.trim() !== "";
	if (step.outputTruncated && hasOutput) lines.push(step.logPath ? t("scriptRun.outputTruncated", { path: step.logPath }) : t("scriptRun.outputTruncatedNoPath"));
	else if (step.logPath && (failed || step.outputTruncated)) lines.push(t("chat.projectSetup.fullOutput", { path: step.logPath }));
	if (!lines.length && !(failed && step.hint)) return null;
	return (
		<div className="flex flex-col gap-0.5 px-1 text-caption leading-relaxed text-muted-foreground select-text">
			{failed && step.hint ? <p className="text-foreground">{step.hint}</p> : null}
			{lines.map((line) => <p className="break-words" key={line}>{line}</p>)}
		</div>
	);
}

/** How long a running step may print nothing before the panel says so. */
export const QUIET_STEP_MS = 3 * 60_000;

/**
 * How long a running step has printed nothing, once that reaches
 * QUIET_STEP_MS, else undefined. Server times only (outputAt): the worker's
 * own clock may be off, and the desktop's is moved onto the control plane's.
 */
function quietStepSilence(step: ScriptRunStep, now: number): number | undefined {
	const silentMs = elapsedSince(step.outputAt, now - desktopClockLeadMs());
	return silentMs !== undefined && silentMs >= QUIET_STEP_MS ? silentMs : undefined;
}

function quietStepText(step: ScriptRunStep, silentMs: number, t: TFunction): string {
	const minutes = Math.floor(silentMs / 60_000);
	return step.timeoutMinutes !== undefined
		? t("scriptRun.quiet", { minutes, limit: step.timeoutMinutes })
		: t("scriptRun.quietNoLimit", { minutes });
}

/**
 * A quiet note under a running step that has printed nothing new for
 * QUIET_STEP_MS: how long, and its time limit, so a long silent step reads as
 * still working rather than stuck. It updates with the panel's clock and goes
 * away once output arrives or the step ends. Screen readers hear it once, from
 * the panel (useQuietStepAnnouncement), not on every minute it counts.
 */
function QuietStepNote({ step, now }: { step: ScriptRunStep; now: number }) {
	const { t } = useTranslation();
	const silentMs = quietStepSilence(step, now);
	if (silentMs === undefined) return null;
	return (
		<p className="flex items-center gap-1.5 px-2.5 pb-1.5 text-caption leading-relaxed text-muted-foreground" data-testid="script-step-quiet">
			<Clock aria-hidden="true" className="size-3 shrink-0 text-passive" />
			<span className="min-w-0">{quietStepText(step, silentMs, t)}</span>
		</p>
	);
}

/**
 * Quiet notes already announced, by run, step and last output. A session can
 * show the same run in up to three panels at once (chat, inspector, startup
 * card); only the first to see a step go quiet announces it. Only the latest
 * QUIET_ANNOUNCEMENTS_KEPT are remembered, oldest forgotten first: a note
 * lasts only while its step stays quiet after the same output.
 */
const announcedQuietSteps = new Set<string>();
const QUIET_ANNOUNCEMENTS_KEPT = 200;

/** Claims a quiet note's announcement; false when a panel already made it. */
export function claimQuietStepAnnouncement(key: string): boolean {
	if (announcedQuietSteps.has(key)) return false;
	announcedQuietSteps.add(key);
	if (announcedQuietSteps.size > QUIET_ANNOUNCEMENTS_KEPT) {
		const oldest = announcedQuietSteps.values().next().value;
		if (oldest !== undefined) announcedQuietSteps.delete(oldest);
	}
	return true;
}

/**
 * The quiet note to announce, said once when a step's note appears: the
 * message stays as first worded while the note's minute count moves on, so the
 * live region does not speak again each minute. The message clears once the
 * note goes (the step writes output or ends, or the panel hides the run).
 */
function useQuietStepAnnouncement(run: ScriptRun, now: number, shown: boolean): string {
	const { t } = useTranslation();
	const step = shown ? run.steps.find((item) => displayStatus(item, run) === "running") : undefined;
	const silentMs = step ? quietStepSilence(step, now) : undefined;
	const key = step && silentMs !== undefined ? `${run.runId}:${step.step}:${step.outputAt ?? ""}` : undefined;
	const text = step && silentMs !== undefined ? quietStepText(step, silentMs, t) : "";
	const latestText = useRef(text);
	latestText.current = text;
	const [message, setMessage] = useState("");
	useEffect(() => {
		if (!key) {
			setMessage("");
			return;
		}
		if (!claimQuietStepAnnouncement(key)) return;
		setMessage(latestText.current);
	}, [key]);
	return message;
}

/**
 * Feeds the desktop clock's lead over the control plane's from this run's
 * events as they arrive live; the events the panel first draws may be old, so
 * they say nothing about the clock.
 */
function useControlPlaneClock(run: ScriptRun): void {
	const seen = useRef<number | undefined>(undefined);
	useEffect(() => {
		const before = seen.current;
		seen.current = run.lastSequence;
		if (before !== undefined && run.lastSequence > before) noteControlPlaneEventArrival(run.lastEventAt);
	}, [run.lastEventAt, run.lastSequence]);
}

function StepRow({
	step,
	run,
	open,
	onToggle,
	now,
}: {
	step: ScriptRunStep;
	run: ScriptRun;
	open: boolean;
	onToggle: () => void;
	now: number;
}) {
	const { t } = useTranslation();
	const bodyId = useId();
	const status = displayStatus(step, run);
	const running = status === "running";
	const reached = status !== "pending" && status !== "skipped";
	const durationMs = running ? elapsedSince(step.startedAt, now) : step.durationMs;
	const statusLabel = t(`scriptRun.stepStatus.${status === "timed_out" ? "timedOut" : status}`);
	const command = step.command || t("scriptRun.stepNumber", { step: step.step });
	const content = (
		<>
			<StepIcon status={status} />
			<span className={cn("min-w-0 flex-1 truncate font-mono text-caption", reached ? "text-foreground" : "text-muted-foreground")} title={step.command || undefined}>
				{command}
			</span>
			<span className="sr-only">{statusLabel}</span>
			{step.repository ? (
				<span className="max-w-[40%] shrink-0 truncate font-mono text-caption text-muted-foreground" title={step.repository}>
					{step.repository}
				</span>
			) : null}
			{durationMs !== undefined && reached ? (
				<span className="shrink-0 font-mono text-caption tabular-nums text-muted-foreground">
					{formatScriptDuration(durationMs)}
					{running && step.timeoutMinutes !== undefined ? (
						// The running step's limit, quieter than its time so far.
						<span className="text-passive">
							<span aria-hidden="true">{" / " + formatScriptDuration(step.timeoutMinutes * 60_000)}</span>
							<span className="sr-only">{t("scriptRun.limit", { limit: formatScriptDuration(step.timeoutMinutes * 60_000) })}</span>
						</span>
					) : null}
				</span>
			) : status === "skipped" ? (
				<span aria-hidden="true" className="shrink-0 text-caption text-passive">{statusLabel}</span>
			) : null}
		</>
	);
	if (!reached) {
		return <li className={cn("flex min-h-8 items-center gap-2 px-2.5 py-1", ROW_PAD_RIGHT)} data-status={status} data-testid="script-step">{content}<span aria-hidden="true" className="size-3 shrink-0" /></li>;
	}
	return (
		<li className="flex flex-col" data-status={status} data-testid="script-step">
			<button
				aria-controls={bodyId}
				aria-expanded={open}
				className={cn("group/script-step flex min-h-8 w-full items-center gap-2 px-2.5 py-1 text-left hover:bg-interactive-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/60", ROW_PAD_RIGHT)}
				onClick={onToggle}
				type="button"
			>
				{content}
				<ChevronRight
					aria-hidden="true"
					className={cn("size-3 shrink-0 text-muted-foreground/60 transition-transform group-hover/script-step:text-muted-foreground", open && "rotate-90")}
				/>
			</button>
			{running ? <QuietStepNote now={now} step={step} /> : null}
			{open ? (
				<div className="flex flex-col gap-1.5 px-2.5 pb-2.5 pt-0.5" id={bodyId}>
					<StepOutput label={t("scriptRun.outputLabel", { step: step.step })} running={running} step={step} />
					<StepNotes step={step} />
				</div>
			) : null}
		</li>
	);
}

/** "Setup" or "Cleanup", in the reader's language. */
export function scriptRunTitle(run: ScriptRun, t: TFunction): string {
	return run.phase === "setup" ? t("scriptRun.setup") : t("scriptRun.cleanup");
}

/** The run's state as the panel header says it ("Running step 2 of 3", "Failed at step 2"). */
export function scriptRunStatusText(run: ScriptRun, t: TFunction): string {
	return run.status === "running"
		? t("scriptRun.running", { step: run.runningStep ?? 1, total: run.total })
		: run.status === "succeeded"
			? t("scriptRun.succeeded")
			: run.status === "failed"
				? t("scriptRun.failedAt", { step: run.failedStep ?? 1 })
				: run.status === "unknown"
					? t("scriptRun.unknown")
					: run.status === "notRun"
						? t("scriptRun.notRun")
						: t("scriptRun.interrupted");
}

/**
 * One Setup or Cleanup run, drawn like a CI job's step log: a header with the
 * run's state and elapsed time, then one row per step that opens onto its
 * output. The running step and failed steps open on their own; a step the
 * reader opened or closed stays that way.
 */
export function ScriptRunPanel({
	run,
	collapsible = false,
	showTitle = true,
	className,
}: {
	run: ScriptRun;
	/** The header toggles the step list (chat timeline); a finished, successful run starts closed. */
	collapsible?: boolean;
	/** Off where the surrounding section already names the phase (the inspector). */
	showTitle?: boolean;
	className?: string;
}) {
	const { t } = useTranslation();
	const listId = useId();
	const live = run.status === "running";
	const now = useNow(live);
	const [toggled, setToggled] = useState<Record<number, boolean>>({});
	const [listToggled, setListToggled] = useState<boolean | undefined>(undefined);
	// A new run (a retry) starts from the defaults again.
	const [seenRunId, setSeenRunId] = useState(run.runId);
	if (seenRunId !== run.runId) {
		setSeenRunId(run.runId);
		setToggled({});
		setListToggled(undefined);
	}
	const listOpen = !collapsible || (listToggled ?? run.status !== "succeeded");
	const [panel, setPanel] = useState<HTMLElement | null>(null);
	const noticeClearance = useHarnessUpdateNoticeClearance(panel);
	const title = scriptRunTitle(run, t);
	const announcement = useStepAnnouncement(run, title);
	useControlPlaneClock(run);
	const quietAnnouncement = useQuietStepAnnouncement(run, now, live && listOpen);
	const summary = scriptRunStatusText(run, t);
	const elapsed = live ? elapsedSince(run.startedAt, now) : run.durationMs;
	const headerIcon = run.status === "running" ? "running" : run.status === "succeeded" ? "succeeded" : run.status === "failed" ? "failed" : "stopped";
	const header = (
		<>
			<StepIcon status={headerIcon} />
			<span className="flex min-w-0 flex-1 items-baseline gap-1.5 text-xs">
				{showTitle ? <span className="shrink-0 font-medium text-foreground">{title}</span> : null}
				<span className={cn("min-w-0 truncate", run.status === "failed" ? "text-error" : "text-muted-foreground")}>{summary}</span>
			</span>
			{elapsed !== undefined ? (
				<span aria-label={t("scriptRun.elapsed", { duration: formatScriptDuration(elapsed) })} className="shrink-0 font-mono text-caption tabular-nums text-muted-foreground">
					{formatScriptDuration(elapsed)}
				</span>
			) : null}
		</>
	);
	return (
		<section
			ref={setPanel}
			aria-label={title}
			className={cn("flex min-w-0 flex-col overflow-hidden rounded-md border bg-surface/60", run.status === "failed" ? "border-error/40" : "border-border", className)}
			data-run-status={run.status}
			data-testid="script-run-panel"
			style={noticeClearance ? { "--script-run-notice-clearance": `${noticeClearance}px` } as CSSProperties : undefined}
		>
			<div aria-atomic="true" aria-live="polite" className="sr-only" data-testid="script-run-announcer" role="status">{announcement}</div>
			<div aria-atomic="true" aria-live="polite" className="sr-only" data-testid="script-run-quiet-announcer" role="status">{quietAnnouncement}</div>
			{collapsible ? (
				<button
					aria-controls={listId}
					aria-expanded={listOpen}
					className={cn("group/script-run flex min-h-8 w-full items-center gap-2 px-2.5 py-1.5 text-left hover:bg-interactive-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/60", ROW_PAD_RIGHT)}
					onClick={() => setListToggled(!listOpen)}
					type="button"
				>
					{header}
					<ChevronRight aria-hidden="true" className={cn("size-3 shrink-0 text-muted-foreground/60 transition-transform group-hover/script-run:text-muted-foreground", listOpen && "rotate-90")} />
				</button>
			) : (
				<div className={cn("flex min-h-8 items-center gap-2 px-2.5 py-1.5", ROW_PAD_RIGHT)}>{header}</div>
			)}
			{listOpen ? (
				<ol className="flex flex-col divide-y divide-border border-t border-border" id={listId}>
					{run.steps.map((step) => {
						const status = displayStatus(step, run);
						const defaultOpen = status === "running" || status === "failed" || status === "timed_out";
						return (
							<StepRow
								key={step.step}
								now={now}
								onToggle={() => setToggled((current) => ({ ...current, [step.step]: !(current[step.step] ?? defaultOpen) }))}
								open={toggled[step.step] ?? defaultOpen}
								run={run}
								step={step}
							/>
						);
					})}
				</ol>
			) : null}
			{listOpen && run.status === "notRun" && run.phase === "cleanup" ? (
				<p className="border-t border-border px-2.5 py-1.5 text-caption leading-relaxed text-muted-foreground">{t("scriptRun.cleanupNotRun")}</p>
			) : null}
		</section>
	);
}
