import { useCallback, useEffect, useRef } from "react";
import { onComposerSent } from "./guided-tour-signals";
import type { OnboardingStep } from "./onboarding-steps";
import type {
	OnboardingAction,
	OnboardingActionOutcome,
	OnboardingFailureReason,
	OnboardingOutcome,
	OnboardingProjectSource,
	OnboardingRunKind,
} from "./onboarding-telemetry-schema";
import { captureRendererEvent } from "./telemetry";

// The desktop onboarding funnel (docs/telemetry.md, "Desktop onboarding
// funnel"). Two layers:
//
// - Steps: each step reports viewed, completed and abandoned at most once per
//   install (once per replay for a Developer Mode replay), so the counts read
//   as a funnel rather than a page-view firehose. Skips and Back are sent once
//   per click.
// - The run: started once per install, resumed on each later launch that
//   reopens it, abandoned when the window closes mid-flow, completed once, and
//   the first message after it (activation).
//
// Every value is from a closed vocabulary (onboarding-telemetry-schema.ts,
// enforced again by the allowlist in telemetry.ts); nothing user-authored is
// sent. Durations are measured here, never read from the UI.

type StepPhase = "viewed" | "completed" | "abandoned";
export type OnboardingPhase = StepPhase;

const STEPS_KEY = "ao.telemetry.onboardingSteps";
const RUN_KEY = "ao.telemetry.onboardingRun";
/** Activation is only looked for this long after onboarding finished. */
const ACTIVATION_WINDOW_MS = 7 * 24 * 60 * 60_000;
/** A project noted by the add or clone handler counts for the tour this long. */
const PROJECT_NOTE_TTL_MS = 60_000;

/** Identifies this launch, so a run reopened on a later launch reads as resumed. */
const LAUNCH_ID = Math.random().toString(36).slice(2);
const memory = new Set<string>();

type RunRecord = {
	kind: OnboardingRunKind;
	startedAt: number;
	lastSeenAt: number;
	launches: number;
	launchId: string;
	step?: OnboardingStep;
	backCount: number;
	autoSkipped: number;
	resumed?: boolean;
	completedAt?: number;
	activated?: boolean;
};

/** A Developer Mode replay: kept in memory, it never outlives its launch. */
let replay: (RunRecord & { id: string; reported: Set<string> }) | null = null;
/** Whether the onboarding window is showing a run right now. */
let flowOpen = false;
let abandonedThisLaunch = false;
const stepStartedAt = new Map<OnboardingStep, number>();

function storage(): Storage | null {
	try {
		return window.localStorage;
	} catch {
		return null;
	}
}

function readJson<T>(key: string): T | null {
	try {
		return JSON.parse(storage()?.getItem(key) ?? "null") as T | null;
	} catch {
		return null;
	}
}

function writeJson(key: string, value: unknown) {
	try {
		storage()?.setItem(key, JSON.stringify(value));
	} catch {
		// Memory still dedupes within this launch.
	}
}

function readFirstRun(): RunRecord | null {
	const record = readJson<RunRecord>(RUN_KEY);
	return record && typeof record.startedAt === "number" ? record : null;
}

function currentRun(): RunRecord | null {
	return replay ?? readFirstRun();
}

function saveRun(record: RunRecord) {
	if (replay && record === replay) return;
	writeJson(RUN_KEY, record);
}

export function currentOnboardingRunKind(): OnboardingRunKind {
	return replay ? "replay" : "first_run";
}

function capture(event: string, properties: Record<string, unknown>) {
	void captureRendererEvent(event, { run_kind: currentOnboardingRunKind(), ...properties });
}

// ── Steps ────────────────────────────────────────────────────────────────────

function readReported(): Set<string> {
	if (replay) return replay.reported;
	try {
		const parsed: unknown = JSON.parse(window.localStorage.getItem(STEPS_KEY) ?? "[]");
		return new Set(Array.isArray(parsed) ? parsed.filter((value): value is string => typeof value === "string") : []);
	} catch {
		return memory;
	}
}

/** Reserves a once-per-install (once-per-replay) slot; false when already taken. */
function reserve(key: string): boolean {
	const reported = readReported();
	if (reported.has(key)) return false;
	reported.add(key);
	if (replay) return true;
	memory.add(key);
	writeJson(STEPS_KEY, [...reported]);
	return true;
}

/** Reserves a (step, phase) slot; false when already reported or contradicted by an earlier phase. */
export function reserveOnboardingPhase(step: OnboardingStep, phase: StepPhase): boolean {
	// A step that finished is never abandoned afterwards.
	if (phase === "abandoned" && readReported().has(`${step}:completed`)) return false;
	return reserve(`${step}:${phase}`);
}

function timeOnStep(step: OnboardingStep): number | undefined {
	const started = stepStartedAt.get(step);
	return started === undefined ? undefined : Date.now() - started;
}

function noteStepShown(step: OnboardingStep) {
	stepStartedAt.set(step, Date.now());
	const run = currentRun();
	if (!run || run.completedAt) return;
	run.lastSeenAt = Date.now();
	if (flowOpen) run.step = step;
	saveRun(run);
}

/**
 * Reports a step phase. `extra` carries the step's own closed properties
 * (what was missing, how much of the welcome story played, where the
 * project came from).
 */
export function reportOnboardingStep(step: OnboardingStep, phase: StepPhase, extra: Record<string, unknown> = {}): void {
	if (phase === "viewed") noteStepShown(step);
	if (!reserveOnboardingPhase(step, phase)) return;
	const timing = phase === "viewed" ? {} : { time_on_step_ms: timeOnStep(step) };
	capture(`ao.onboarding.step_${phase}`, { step, ...extra, ...timing });
}

/**
 * Records completion of a step this install has already seen. Completion can be
 * observed long after the step's UI is gone (GitHub sign-in finished in a
 * terminal, outside the app, or on a later launch), so it is keyed to the
 * install's recorded view rather than to the component that showed it.
 */
export function completeOnboardingStepIfViewed(step: OnboardingStep): void {
	if (!readReported().has(`${step}:viewed`)) return;
	reportOnboardingStep(step, "completed");
}

/**
 * A step passed over. `already_done` (nothing to do there, so the flow went
 * past it) is counted once per install; a person skipping or dismissing is
 * counted on every click.
 */
export function skipOnboardingStep(step: OnboardingStep, reason: "already_done" | "user" | "dismissed"): void {
	if (reason === "already_done") {
		if (!reserve(`${step}:skipped`)) return;
		const run = currentRun();
		if (run && !run.completedAt) {
			run.autoSkipped += 1;
			saveRun(run);
		}
		capture("ao.onboarding.step_skipped", { step, reason });
		return;
	}
	capture("ao.onboarding.step_skipped", { step, reason, time_on_step_ms: timeOnStep(step) });
}

/** Back, from one page to an earlier one. Sent per click. */
export function reportOnboardingBack(from: OnboardingStep, to: OnboardingStep): void {
	const run = currentRun();
	if (run && !run.completedAt) {
		run.backCount += 1;
		saveRun(run);
	}
	capture("ao.onboarding.back_clicked", { step: from, to_step: to });
}

/**
 * Reports a step as viewed while `active`, abandoned if the window closes while
 * it is still active, and completed when it goes from active to inactive. The
 * returned function reports completion explicitly for steps whose component
 * unmounts as part of finishing. Pass `autoComplete: false` for a page that
 * can be left by Back, which reports its own completion.
 */
export function useOnboardingStep(
	step: OnboardingStep,
	active: boolean,
	options: { autoComplete?: boolean; viewed?: Record<string, unknown> } = {},
): (extra?: Record<string, unknown>) => void {
	const wasActive = useRef(false);
	const autoComplete = options.autoComplete ?? true;
	const viewed = useRef(options.viewed);
	viewed.current = options.viewed;
	useEffect(() => {
		if (!active) {
			if (wasActive.current && autoComplete) reportOnboardingStep(step, "completed");
			wasActive.current = false;
			return;
		}
		wasActive.current = true;
		reportOnboardingStep(step, "viewed", viewed.current);
		const onHide = () => reportOnboardingStep(step, "abandoned");
		window.addEventListener("pagehide", onHide);
		return () => window.removeEventListener("pagehide", onHide);
	}, [step, active, autoComplete]);
	return useCallback((extra?: Record<string, unknown>) => reportOnboardingStep(step, "completed", extra), [step]);
}

// ── The run ─────────────────────────────────────────────────────────────────

function abandonRun() {
	if (!flowOpen || abandonedThisLaunch) return;
	const run = currentRun();
	if (!run || run.completedAt) return;
	abandonedThisLaunch = true;
	run.lastSeenAt = Date.now();
	saveRun(run);
	capture("ao.onboarding.abandoned", { step: run.step, duration_ms: Date.now() - run.startedAt, launch_count: run.launches });
}

/**
 * The onboarding window opened on `step`. A first run is started once per
 * install and resumed on every later launch that reopens it; a replay starts
 * a new run each time. Returns the cleanup for when the window closes.
 */
export function beginOnboardingRun(kind: OnboardingRunKind, step: OnboardingStep): () => void {
	const now = Date.now();
	flowOpen = true;
	if (kind === "replay") {
		replay = {
			id: LAUNCH_ID + now,
			reported: new Set(),
			kind,
			startedAt: now,
			lastSeenAt: now,
			launches: 1,
			launchId: LAUNCH_ID,
			step,
			backCount: 0,
			autoSkipped: 0,
		};
		stepStartedAt.clear();
		capture("ao.onboarding.started", { step });
	} else {
		const existing = readFirstRun();
		if (!existing) {
			saveRun({ kind, startedAt: now, lastSeenAt: now, launches: 1, launchId: LAUNCH_ID, step, backCount: 0, autoSkipped: 0 });
			capture("ao.onboarding.started", { step });
		} else if (!existing.completedAt && existing.launchId !== LAUNCH_ID) {
			const away = now - existing.lastSeenAt;
			const resumed = { ...existing, launches: existing.launches + 1, launchId: LAUNCH_ID, lastSeenAt: now, step, resumed: true };
			saveRun(resumed);
			capture("ao.onboarding.resumed", { step, away_ms: away, launch_count: resumed.launches });
		}
	}
	window.addEventListener("pagehide", abandonRun);
	return () => {
		window.removeEventListener("pagehide", abandonRun);
		flowOpen = false;
	};
}

/** The run ended: the last page's Continue (`finished`), Skip setup, or closed for the person (`superseded`). */
export function completeOnboardingRun(outcome: OnboardingOutcome): void {
	const run = currentRun();
	if (!run || run.completedAt) return;
	const now = Date.now();
	run.completedAt = now;
	saveRun(run);
	flowOpen = false;
	capture("ao.onboarding.completed", {
		step: run.step,
		outcome,
		duration_ms: now - run.startedAt,
		launch_count: run.launches,
		resumed: run.resumed === true,
		back_count: run.backCount,
		auto_skipped_count: run.autoSkipped,
	});
}

/**
 * Onboarding was marked seen without being shown again (a project, or an
 * offer to migrate an older installation, appeared before a later launch
 * reopened it). Ends a first run still in progress; a first launch that
 * never showed it sends nothing.
 */
export function supersedeOnboardingRun(): void {
	if (replay) return;
	const run = readFirstRun();
	if (!run || run.completedAt) return;
	completeOnboardingRun("superseded");
}

// ── Actions: installs, sign-ins, project clone and add ─────────────────────

const attempts = new Map<string, number>();
const pending = new Map<string, { attempt: number; startedAt: number; step?: OnboardingStep }>();

function actionKey(action: OnboardingAction, target?: string) {
	return `${action}:${target ?? ""}`;
}

/** Something the person (or the page, `auto`) started that can fail and be retried. Attempts count per launch. */
export function startOnboardingAction(
	action: OnboardingAction,
	options: { target?: string; step?: OnboardingStep; auto?: boolean } = {},
): void {
	const key = actionKey(action, options.target);
	const attempt = (attempts.get(key) ?? 0) + 1;
	attempts.set(key, attempt);
	pending.set(key, { attempt, startedAt: Date.now(), step: options.step });
	capture("ao.onboarding.action_started", { action, target: options.target, step: options.step, attempt, auto: options.auto });
}

/** Ends the attempt in progress for (action, target), once; nothing when none is. Returns whether it reported. */
export function finishOnboardingAction(
	action: OnboardingAction,
	outcome: OnboardingActionOutcome,
	options: { target?: string; reason?: OnboardingFailureReason; errorCode?: string } = {},
): boolean {
	const key = actionKey(action, options.target);
	const attempt = pending.get(key);
	if (!attempt) return false;
	pending.delete(key);
	capture("ao.onboarding.action_finished", {
		action,
		target: options.target,
		step: attempt.step,
		attempt: attempt.attempt,
		outcome,
		reason: options.reason,
		error_code: options.errorCode,
		duration_ms: Date.now() - attempt.startedAt,
	});
	return true;
}

export function onboardingActionPending(action: OnboardingAction, target?: string): boolean {
	return pending.has(actionKey(action, target));
}

/** An install job's end state, as an action outcome. */
export function installJobOutcome(status: string | undefined): { outcome: OnboardingActionOutcome; reason?: OnboardingFailureReason } | null {
	switch (status) {
		case "succeeded":
			return { outcome: "succeeded" };
		case "failed":
			return { outcome: "failed", reason: "install_failed" };
		case "interrupted":
			return { outcome: "failed", reason: "interrupted" };
		case "unsupported":
			return { outcome: "failed", reason: "unsupported" };
		default:
			return null;
	}
}

/** The API error code on a failed response body, never its message. */
export function apiErrorCode(error: unknown): string | undefined {
	const code = (error as { code?: unknown } | null | undefined)?.code;
	return typeof code === "string" ? code : undefined;
}

// ── The guided tour's project step ──────────────────────────────────────────

let tourStep: OnboardingStep | undefined;
let projectNote: { source: "clone" | "local_folder"; at: number } | undefined;

/** The tour step showing now, or undefined; set by GuidedTour. */
export function setOnboardingTourStep(step: OnboardingStep | undefined): void {
	tourStep = step;
}

function reportProjectResult(action: "project_clone" | "project_add", outcome: OnboardingActionOutcome, errorCode?: string) {
	if (tourStep !== "tour_project") return;
	startOnboardingAction(action, { step: "tour_project" });
	finishOnboardingAction(action, outcome, outcome === "failed" ? { reason: errorCode ? "refused" : "request_failed", errorCode } : {});
}

/** A project was cloned or added; the tour's project step reads where it came from. */
export function reportOnboardingProjectAdded(source: "clone" | "local_folder"): void {
	projectNote = { source, at: Date.now() };
	reportProjectResult(source === "clone" ? "project_clone" : "project_add", "succeeded");
}

/** A clone or add failed while the tour's project step was showing. */
export function reportOnboardingProjectFailed(action: "project_clone" | "project_add", errorCode: string | undefined): void {
	reportProjectResult(action, "failed", errorCode && errorCode !== "unknown" ? errorCode : undefined);
}

/** Where the project that ended the tour's project step came from. */
export function onboardingProjectSource(isNew: boolean): OnboardingProjectSource {
	const note = projectNote;
	projectNote = undefined;
	if (note && Date.now() - note.at < PROJECT_NOTE_TTL_MS) return note.source;
	return isNew ? "other" : "existing";
}

// ── Activation ──────────────────────────────────────────────────────────────

/** The first chat message delivered after a first run finished, within a week, once per install. */
export function recordOnboardingActivation(duringTour: boolean, now = Date.now()): void {
	if (replay) return;
	const run = readFirstRun();
	if (!run?.completedAt || run.activated) return;
	if (now - run.completedAt > ACTIVATION_WINDOW_MS) return;
	run.activated = true;
	saveRun(run);
	void captureRendererEvent("ao.onboarding.activated", {
		milestone: "first_message",
		time_since_completed_ms: now - run.completedAt,
		during_tour: duringTour,
	});
}

/** Listens for the first delivered chat message for activation. Mounted once, by GuidedTour. */
export function useOnboardingActivation(duringTour: () => boolean): void {
	const duringTourRef = useRef(duringTour);
	duringTourRef.current = duringTour;
	useEffect(() => onComposerSent(() => recordOnboardingActivation(duringTourRef.current())), []);
}

/** Test seam: forgets this launch's in-memory state. */
export function resetOnboardingTelemetryForTests(): void {
	memory.clear();
	replay = null;
	flowOpen = false;
	abandonedThisLaunch = false;
	stepStartedAt.clear();
	attempts.clear();
	pending.clear();
	tourStep = undefined;
	projectNote = undefined;
}
