import type { CloudCpClientEvent } from "./cloud-cp";

/**
 * Live Setup and Cleanup script logs (`workspace.script.step`).
 *
 * The worker publishes one event when a step starts, coalesced updates while it
 * runs (each carrying the latest output tail), one final event per step and,
 * after a failure, `skipped` for the steps that never ran. This module turns
 * those events into one view model per run; the panel renders it as-is.
 *
 * The payload type is declared here rather than read from the generated cloud
 * client schema so the renderer stays tolerant of fields added later.
 */
export const SCRIPT_STEP_EVENT = "workspace.script.step";

/**
 * Session events that only follow a finished Setup run: the worker's
 * `workspace.setup` summary, sent once the run ends, and `agent.ready`, which
 * the worker sends only after Setup succeeded. A run still unfinished before
 * one of them lost its final event and will not move again.
 */
export const SCRIPT_RUN_END_EVENTS: ReadonlySet<string> = new Set(["workspace.setup", "agent.ready"]);

export type ScriptPhase = "setup" | "cleanup";
export type ScriptStepStatus = "running" | "succeeded" | "failed" | "timed_out" | "skipped";

/** The `workspace.script.step` payload, as the contract defines it. */
export interface ScriptStepEventPayload {
	phase: ScriptPhase;
	runId: string;
	/** 1-based. */
	step: number;
	total: number;
	/** One line, redacted, at most 300 bytes. */
	command: string;
	/** Folder of the repository the step ran in; "" is the main repository. */
	repository: string;
	status: ScriptStepStatus;
	exitCode?: number;
	startedAt?: string;
	durationMs?: number;
	/** Redacted, ANSI-stripped tail of combined stdout and stderr (at most 16 KiB). */
	output?: string;
	outputTruncated?: boolean;
	/**
	 * The reader may not see this step's output (a share grantee outside the
	 * organization): `output` is empty and the user, groups, folder and log
	 * path are left out.
	 */
	outputHidden?: boolean;
	logPath?: string;
	user?: string;
	groups?: string[];
	dir?: string;
	hint?: string;
	/** The step's time limit in minutes (1 to 120); absent when not known (an older worker or Cleanup hook). */
	timeoutMinutes?: number;
	/**
	 * On a running step's live update: when the step last wrote any output, by
	 * the control plane's clock (RFC 3339). It moves even when the output's tail
	 * looks the same (a repeated line, a progress line redrawn with \r). Absent
	 * from an older control plane or worker.
	 */
	outputAt?: string;
}

/** A step slot. `pending` is a step the run has not reached (no event yet). */
export interface ScriptRunStep {
	step: number;
	status: ScriptStepStatus | "pending";
	command: string;
	repository: string;
	exitCode?: number;
	startedAt?: string;
	durationMs?: number;
	output: string;
	outputTruncated: boolean;
	/** The output is withheld from this reader; see {@link ScriptStepEventPayload.outputHidden}. */
	outputHidden: boolean;
	logPath?: string;
	user?: string;
	groups?: string[];
	dir?: string;
	hint?: string;
	/** See {@link ScriptStepEventPayload.timeoutMinutes}. */
	timeoutMinutes?: number;
	/**
	 * While the step runs: when it last wrote any output, by the control
	 * plane's clock. The newest event's outputAt when it carries one (the
	 * worker updates a step that keeps writing at least every minute), else
	 * when that event arrived (an older worker sends an update only when the
	 * output changes). Both are server times: a worker's clock never counts.
	 */
	outputAt?: string;
}

/**
 * `interrupted`: the run never finished and a later run of the same phase
 * started (a worker restart or a retry), so its running step will not end.
 * `unknown`: the run never reported its end, but the session moved past it
 * (a later Setup summary or agent start, or the session's sandbox is gone),
 * so it is no longer running and its outcome was never received.
 * `notRun`: every step was reported skipped before any ran (a Cleanup the
 * control plane could not run before deleting the sandbox).
 */
export type ScriptRunStatus = "running" | "succeeded" | "failed" | "interrupted" | "unknown" | "notRun";

export interface GroupScriptRunsOptions {
	/** The session's sandbox is gone or AO stopped starting it: nothing runs any more. */
	sessionEnded?: boolean;
}

/**
 * True once a Cloud session's sandbox is deleted or AO gave up starting it, so
 * no Setup or Cleanup step of it can still be running. A session being deleted
 * is not ended yet: its Cleanup runs then.
 */
export function cloudSessionScriptsEnded(cloud: { observedState?: string; runtimeState?: string } | undefined): boolean {
	if (!cloud) return false;
	return cloud.observedState === "deleted" || cloud.observedState === "terminated" || cloud.runtimeState === "terminated";
}

export interface ScriptRun {
	runId: string;
	phase: ScriptPhase;
	total: number;
	steps: ScriptRunStep[];
	status: ScriptRunStatus;
	/** The step that failed or timed out, when the run failed. */
	failedStep?: number;
	/** The step running now, while the run is running. */
	runningStep?: number;
	startedAt?: string;
	/** Wall time of a finished run, from its first step's start to its last step's end. */
	durationMs?: number;
	/** Sequence of the run's first event: where it sits in the session's history. */
	firstSequence: number;
	/** Sequence of its newest event: changes whenever the run does. */
	lastSequence: number;
	/** When the control plane recorded its newest event (control plane time). */
	lastEventAt?: string;
}

const statuses = new Set<ScriptStepStatus>(["running", "succeeded", "failed", "timed_out", "skipped"]);

const text = (value: unknown): string | undefined => (typeof value === "string" ? value : undefined);
const nonEmpty = (value: unknown): string | undefined => {
	const result = text(value)?.trim();
	return result ? result : undefined;
};
const integer = (value: unknown): number | undefined =>
	typeof value === "number" && Number.isFinite(value) ? Math.trunc(value) : undefined;

/** Reads a `workspace.script.step` payload, or undefined when it is not one. */
export function parseScriptStepPayload(payload: unknown): ScriptStepEventPayload | undefined {
	if (!payload || typeof payload !== "object") return undefined;
	const value = payload as Record<string, unknown>;
	const phase = value.phase === "setup" || value.phase === "cleanup" ? value.phase : undefined;
	const runId = nonEmpty(value.runId);
	const step = integer(value.step);
	const status = typeof value.status === "string" && statuses.has(value.status as ScriptStepStatus) ? value.status as ScriptStepStatus : undefined;
	if (!phase || !runId || step === undefined || step < 1 || !status) return undefined;
	const total = Math.max(integer(value.total) ?? step, step);
	const groups = Array.isArray(value.groups) ? value.groups.filter((group): group is string => typeof group === "string" && group !== "") : undefined;
	return {
		phase, runId, step, total, status,
		command: text(value.command) ?? "",
		repository: text(value.repository) ?? "",
		exitCode: integer(value.exitCode),
		startedAt: nonEmpty(value.startedAt),
		durationMs: integer(value.durationMs),
		output: text(value.output),
		outputTruncated: value.outputTruncated === true,
		outputHidden: value.outputHidden === true,
		logPath: nonEmpty(value.logPath),
		user: nonEmpty(value.user),
		groups: groups && groups.length > 0 ? groups : undefined,
		dir: nonEmpty(value.dir),
		hint: nonEmpty(value.hint),
		timeoutMinutes: timeoutMinutes(value.timeoutMinutes),
		outputAt: timestamp(value.outputAt),
	};
}

/** An RFC 3339 time the payload carries, else undefined. */
function timestamp(value: unknown): string | undefined {
	const at = nonEmpty(value);
	return at !== undefined && Number.isFinite(Date.parse(at)) ? at : undefined;
}

/** A step's time limit as the control plane bounds it (1 to 120 minutes), else undefined. */
function timeoutMinutes(value: unknown): number | undefined {
	const minutes = integer(value);
	return minutes !== undefined && minutes >= 1 && minutes <= 120 ? minutes : undefined;
}

/** True for a step that has ended (any outcome but running or not started). */
export function scriptStepFinished(status: ScriptRunStep["status"]): boolean {
	return status === "succeeded" || status === "failed" || status === "timed_out" || status === "skipped";
}

function pendingStep(step: number): ScriptRunStep {
	return { step, status: "pending", command: "", repository: "", output: "", outputTruncated: false, outputHidden: false };
}

function timeOf(value: string | undefined): number | undefined {
	if (!value) return undefined;
	const parsed = Date.parse(value);
	return Number.isFinite(parsed) ? parsed : undefined;
}

/**
 * Groups `workspace.script.step` events by `runId`, oldest run first. The
 * newest event per (runId, step) wins: each carries the latest output tail, so
 * nothing is accumulated. Other event types are ignored, except
 * {@link SCRIPT_RUN_END_EVENTS}, so callers can pass a session's whole event
 * list.
 */
export function groupScriptRuns(events: readonly CloudCpClientEvent[], { sessionEnded = false }: GroupScriptRunsOptions = {}): ScriptRun[] {
	const runs = new Map<string, {
		phase: ScriptPhase;
		total: number;
		latest: Map<number, { sequence: number; payload: ScriptStepEventPayload; createdAt: string }>;
		firstSequence: number;
		lastSequence: number;
		firstCreatedAt: string;
		lastCreatedAt: string;
	}>();
	let lastEndSequence = -Infinity;
	for (const event of events) {
		if (SCRIPT_RUN_END_EVENTS.has(event.type)) lastEndSequence = Math.max(lastEndSequence, event.sequence);
		if (event.type !== SCRIPT_STEP_EVENT) continue;
		const payload = parseScriptStepPayload(event.payload);
		if (!payload) continue;
		let run = runs.get(payload.runId);
		if (!run) {
			run = {
				phase: payload.phase, total: 0, latest: new Map(), firstSequence: event.sequence, lastSequence: event.sequence,
				firstCreatedAt: event.createdAt, lastCreatedAt: event.createdAt,
			};
			runs.set(payload.runId, run);
		}
		run.total = Math.max(run.total, payload.total);
		run.firstSequence = Math.min(run.firstSequence, event.sequence);
		if (event.sequence >= run.lastSequence) {
			run.lastSequence = event.sequence;
			run.lastCreatedAt = event.createdAt;
		}
		const previous = run.latest.get(payload.step);
		if (!previous || event.sequence >= previous.sequence) run.latest.set(payload.step, { sequence: event.sequence, payload, createdAt: event.createdAt });
	}
	const ordered = [...runs.entries()].sort((a, b) => a[1].firstSequence - b[1].firstSequence);
	return ordered.map(([runId, run], index) => {
		const steps: ScriptRunStep[] = [];
		for (let step = 1; step <= run.total; step++) {
			const latest = run.latest.get(step);
			const payload = latest?.payload;
			if (!payload) {
				steps.push(pendingStep(step));
				continue;
			}
			const finished = scriptStepFinished(payload.status);
			const failed = payload.status === "failed" || payload.status === "timed_out";
			steps.push({
				step,
				status: payload.status,
				command: payload.command,
				repository: payload.repository,
				exitCode: finished ? payload.exitCode : undefined,
				startedAt: payload.startedAt,
				durationMs: finished ? payload.durationMs : undefined,
				output: payload.outputHidden ? "" : payload.output ?? "",
				outputTruncated: payload.outputTruncated === true,
				outputHidden: payload.outputHidden === true,
				// Withheld with the output, whatever an older control plane sent.
				logPath: payload.outputHidden ? undefined : payload.logPath,
				user: failed && !payload.outputHidden ? payload.user : undefined,
				groups: failed && !payload.outputHidden ? payload.groups : undefined,
				dir: failed && !payload.outputHidden ? payload.dir : undefined,
				hint: failed ? payload.hint : undefined,
				timeoutMinutes: payload.timeoutMinutes,
				outputAt: payload.status === "running" ? payload.outputAt ?? latest?.createdAt : undefined,
			});
		}
		const failedStep = steps.find((step) => step.status === "failed" || step.status === "timed_out")?.step;
		const allSucceeded = steps.length > 0 && steps.every((step) => step.status === "succeeded");
		const noneRan = steps.length > 0 && steps.every((step) => step.status === "skipped");
		const superseded = ordered.slice(index + 1).some(([, later]) => later.phase === run.phase);
		// Its final event was lost: the session has moved on past the run.
		const ended = sessionEnded || lastEndSequence > run.lastSequence;
		const status: ScriptRunStatus = failedStep !== undefined ? "failed"
			: allSucceeded ? "succeeded"
			: noneRan ? "notRun"
			: superseded ? "interrupted"
			: ended ? "unknown"
			: "running";
		const runningStep = status === "running"
			? (steps.find((step) => step.status === "running") ?? steps.find((step) => step.status === "pending"))?.step
			: undefined;
		const startedAt = steps.find((step) => step.startedAt)?.startedAt ?? run.firstCreatedAt;
		let durationMs: number | undefined;
		if (status === "succeeded" || status === "failed") {
			const start = timeOf(startedAt);
			const ends = steps.flatMap((step) => {
				const stepStart = timeOf(step.startedAt);
				return stepStart !== undefined && step.durationMs !== undefined ? [stepStart + step.durationMs] : [];
			});
			const timed = steps.filter((step) => step.durationMs !== undefined);
			// An old Cleanup hook reports no times at all: show none rather than 0:00.
			durationMs = start !== undefined && ends.length > 0
				? Math.max(0, Math.max(...ends) - start)
				: timed.length > 0 ? timed.reduce((sum, step) => sum + step.durationMs!, 0) : undefined;
		}
		return {
			runId, phase: run.phase, total: run.total, steps, status, failedStep, runningStep, startedAt, durationMs,
			firstSequence: run.firstSequence, lastSequence: run.lastSequence, lastEventAt: timestamp(run.lastCreatedAt),
		};
	});
}

/** How many recent live arrivals bound the desktop clock's lead. */
const CLOCK_SAMPLES = 8;
/** A lead this small is delivery time or harmless drift, not a clock to correct. */
const CLOCK_TOLERANCE_MS = 60_000;
let clockSamples: number[] = [];

/**
 * Records that an event the control plane created at `createdAt` (its clock)
 * reached the desktop just now, live. The control plane cannot have created it
 * later than its own now, so the arrival time minus `createdAt` bounds how far
 * the desktop clock runs ahead of the control plane's.
 */
export function noteControlPlaneEventArrival(createdAt: string | undefined, arrivedAt = Date.now()): void {
	const created = timeOf(createdAt);
	if (created === undefined) return;
	clockSamples = [...clockSamples, arrivedAt - created].slice(-CLOCK_SAMPLES);
}

/**
 * How far the desktop clock runs ahead of the control plane's (negative when
 * behind), from the tightest recent live arrival; 0 until one is seen or while
 * the two are within a minute of each other.
 */
export function desktopClockLeadMs(): number {
	if (clockSamples.length === 0) return 0;
	const lead = Math.min(...clockSamples);
	return Math.abs(lead) <= CLOCK_TOLERANCE_MS ? 0 : lead;
}

/** Forgets what arrivals said about the desktop clock (tests). */
export function forgetControlPlaneClock(): void {
	clockSamples = [];
}

/** The newest run of a phase, if any. */
export function latestScriptRun(runs: readonly ScriptRun[], phase: ScriptPhase): ScriptRun | undefined {
	for (let index = runs.length - 1; index >= 0; index--) {
		if (runs[index].phase === phase) return runs[index];
	}
	return undefined;
}

/** Keeps only the events {@link groupScriptRuns} reads, so a small cache can hold them. */
export function scriptStepEvents(events: readonly CloudCpClientEvent[]): CloudCpClientEvent[] {
	return events.filter((event) => event.type === SCRIPT_STEP_EVENT || SCRIPT_RUN_END_EVENTS.has(event.type));
}

/** The cache key of an event it keeps: one per (runId, step), and one end marker. */
const END_MARKER_KEY = "\u0000end";

function stepKey(event: CloudCpClientEvent): string | undefined {
	if (SCRIPT_RUN_END_EVENTS.has(event.type)) return END_MARKER_KEY;
	const payload = parseScriptStepPayload(event.payload);
	return payload ? `${payload.runId}\u0000${payload.step}` : undefined;
}

/**
 * Merges events into a sequence-ordered list that keeps only the newest event
 * per (runId, step): every event carries the step's latest output tail, so
 * older live snapshots of a step are never read again and are dropped rather
 * than held for the session's lifetime. Of the run-ending events it keeps the
 * newest, all {@link groupScriptRuns} needs. Returns `existing` unchanged when
 * nothing newer arrived.
 */
export function mergeScriptStepEvents(existing: readonly CloudCpClientEvent[], incoming: readonly CloudCpClientEvent[]): CloudCpClientEvent[] {
	const fresh = scriptStepEvents(incoming);
	if (fresh.length === 0) return existing as CloudCpClientEvent[];
	const latest = new Map<string, CloudCpClientEvent>();
	for (const event of existing) {
		const key = stepKey(event);
		const current = key ? latest.get(key) : undefined;
		if (key && (!current || event.sequence > current.sequence)) latest.set(key, event);
	}
	let changed = latest.size !== existing.length;
	for (const event of fresh) {
		const key = stepKey(event);
		if (!key) continue;
		const current = latest.get(key);
		if (current && current.sequence >= event.sequence) continue;
		latest.set(key, event);
		changed = true;
	}
	if (!changed) return existing as CloudCpClientEvent[];
	return [...latest.values()].sort((a, b) => a.sequence - b.sequence);
}

/** m:ss, or h:mm:ss past an hour, as GitHub Actions shows step times. */
export function formatScriptDuration(durationMs: number): string {
	const total = Math.max(0, Math.floor(durationMs / 1000));
	const hours = Math.floor(total / 3600);
	const minutes = Math.floor((total % 3600) / 60);
	const seconds = String(total % 60).padStart(2, "0");
	return hours > 0 ? `${hours}:${String(minutes).padStart(2, "0")}:${seconds}` : `${minutes}:${seconds}`;
}
