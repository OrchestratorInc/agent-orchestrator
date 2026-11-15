import type { CloudCpClientEvent } from "./cloud-cp";

export const SCRIPT_STEP_EVENT = "workspace.script.step";

export const SCRIPT_RUN_END_EVENTS: ReadonlySet<string> = new Set(["workspace.setup", "agent.ready"]);

export type ScriptPhase = "setup" | "cleanup";
export type ScriptStepStatus = "running" | "succeeded" | "failed" | "timed_out" | "skipped";

export interface ScriptStepEventPayload {
	phase: ScriptPhase;
	runId: string;
	step: number;
	total: number;
	command: string;
	repository: string;
	status: ScriptStepStatus;
	exitCode?: number;
	startedAt?: string;
	durationMs?: number;
	output?: string;
	outputTruncated?: boolean;
	outputHidden?: boolean;
	logPath?: string;
	user?: string;
	groups?: string[];
	dir?: string;
	hint?: string;
	timeoutMinutes?: number;
	outputAt?: string;
}

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
	outputHidden: boolean;
	logPath?: string;
	user?: string;
	groups?: string[];
	dir?: string;
	hint?: string;
	timeoutMinutes?: number;
	outputAt?: string;
}

export type ScriptRunStatus = "running" | "succeeded" | "failed" | "interrupted" | "unknown" | "notRun";

export interface GroupScriptRunsOptions {
	sessionEnded?: boolean;
}

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
	failedStep?: number;
	runningStep?: number;
	startedAt?: string;
	durationMs?: number;
	firstSequence: number;
	lastSequence: number;
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

function timestamp(value: unknown): string | undefined {
	const at = nonEmpty(value);
	return at !== undefined && Number.isFinite(Date.parse(at)) ? at : undefined;
}

function timeoutMinutes(value: unknown): number | undefined {
	const minutes = integer(value);
	return minutes !== undefined && minutes >= 1 && minutes <= 120 ? minutes : undefined;
}

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

const CLOCK_SAMPLES = 8;
const CLOCK_TOLERANCE_MS = 60_000;
let clockSamples: number[] = [];

export function noteControlPlaneEventArrival(createdAt: string | undefined, arrivedAt = Date.now()): void {
	const created = timeOf(createdAt);
	if (created === undefined) return;
	clockSamples = [...clockSamples, arrivedAt - created].slice(-CLOCK_SAMPLES);
}

export function desktopClockLeadMs(): number {
	if (clockSamples.length === 0) return 0;
	const lead = Math.min(...clockSamples);
	return Math.abs(lead) <= CLOCK_TOLERANCE_MS ? 0 : lead;
}

export function forgetControlPlaneClock(): void {
	clockSamples = [];
}

export function latestScriptRun(runs: readonly ScriptRun[], phase: ScriptPhase): ScriptRun | undefined {
	for (let index = runs.length - 1; index >= 0; index--) {
		if (runs[index].phase === phase) return runs[index];
	}
	return undefined;
}

export function scriptStepEvents(events: readonly CloudCpClientEvent[]): CloudCpClientEvent[] {
	return events.filter((event) => event.type === SCRIPT_STEP_EVENT || SCRIPT_RUN_END_EVENTS.has(event.type));
}

const END_MARKER_KEY = "\u0000end";

function stepKey(event: CloudCpClientEvent): string | undefined {
	if (SCRIPT_RUN_END_EVENTS.has(event.type)) return END_MARKER_KEY;
	const payload = parseScriptStepPayload(event.payload);
	return payload ? `${payload.runId}\u0000${payload.step}` : undefined;
}

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

export function formatScriptDuration(durationMs: number): string {
	const total = Math.max(0, Math.floor(durationMs / 1000));
	const hours = Math.floor(total / 3600);
	const minutes = Math.floor((total % 3600) / 60);
	const seconds = String(total % 60).padStart(2, "0");
	return hours > 0 ? `${hours}:${String(minutes).padStart(2, "0")}:${seconds}` : `${minutes}:${seconds}`;
}
