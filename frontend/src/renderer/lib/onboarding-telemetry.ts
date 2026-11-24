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
	OnboardingSkipReason,
} from "./onboarding-telemetry-schema";
import { isAnalyticsOptedOut, onAnalyticsOptedOut } from "./analytics-opt-out";
import { captureRendererEvent, captureRendererEventOnUnload } from "./telemetry";
import { captureOnboardingFailureToSentry } from "./sentry";
import { noteOnboardingPagesFinished, onboardingInProgress, onboardingWindowOpen, resetOnboardingFlowForTests, setOnboardingWindowOpen } from "./onboarding-flow";

export { onboardingInProgress, setOnboardingTourStep } from "./onboarding-flow";

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
//
// While analytics is opted out nothing here is counted or stored, and an
// opt-out drops the run, attempts and timings already held, so nothing from
// the opted-out period reaches an event sent after a later opt-in.
//
// Sentry follows the separate Share error events choice, enforced in Electron
// main: an install or sign-in started during onboarding that then breaks is
// one Sentry issue per attempt, and every renderer Sentry event while the
// first-run window shows, or the tour in the launch its pages finished, is
// tagged flow:onboarding (onboarding-flow.ts). Only the started actions and
// an id per attempt are held for that, in memory, whatever the analytics
// choice.

type StepPhase = "viewed" | "completed" | "abandoned";
export type OnboardingPhase = StepPhase;

const STEPS_KEY = "ao.telemetry.onboardingSteps";
const RUN_KEY = "ao.telemetry.onboardingRun";
/** Activation is only looked for this long after onboarding finished. */
const ACTIVATION_WINDOW_MS = 7 * 24 * 60 * 60_000;
/** How long after onboarding the first message still counts as activation. */
export const ONBOARDING_ACTIVATION_WINDOW_MS = ACTIVATION_WINDOW_MS;
/** A project noted by the add or clone handler counts for the tour this long. */
const PROJECT_NOTE_TTL_MS = 60_000;

/** Identifies this launch, so a run reopened on a later launch reads as resumed. */
const LAUNCH_ID = Math.random().toString(36).slice(2);
const memory = new Set<string>();
/** The pages whose position the run reports; sub-steps such as github_auth show inside one. */
const RUN_PAGES: ReadonlySet<OnboardingStep> = new Set(["welcome", "sign_in", "tools"]);

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
let abandonedThisLaunch = false;
/** This launch's latest first-run record, for when storage can't be written. */
let memoryRun: RunRecord | null = null;
/** An opt-out dropped the stored run this launch; a copy storage failed to remove must not come back. */
let storedRunDiscarded = false;
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
	// Written this launch: newer than storage, and kept when storage refused it.
	if (memoryRun) return { ...memoryRun };
	if (storedRunDiscarded) return null;
	const record = readJson<RunRecord>(RUN_KEY);
	return record && typeof record.startedAt === "number" ? record : null;
}

function currentRun(): RunRecord | null {
	return replay ?? readFirstRun();
}

function saveRun(record: RunRecord) {
	if (replay && record === replay) return;
	memoryRun = { ...record };
	writeJson(RUN_KEY, record);
}

export function currentOnboardingRunKind(): OnboardingRunKind {
	return replay ? "replay" : "first_run";
}

function capture(event: string, properties: Record<string, unknown>) {
	void captureRendererEvent(event, { run_kind: currentOnboardingRunKind(), ...properties });
}

/** For pagehide listeners: a queued event would be lost with the page (captureRendererEventOnUnload). */
function captureOnUnload(event: string, properties: Record<string, unknown>) {
	captureRendererEventOnUnload(event, { run_kind: currentOnboardingRunKind(), ...properties });
}

/** False while analytics is opted out: nothing is counted, stored or sent then. */
function tracking(): boolean {
	return !isAnalyticsOptedOut();
}

// ── Steps ────────────────────────────────────────────────────────────────────

function readReported(): Set<string> {
	if (replay) return replay.reported;
	try {
		const parsed: unknown = JSON.parse(window.localStorage.getItem(STEPS_KEY) ?? "[]");
		// Plus this launch's own, in case storage refused a write.
		return new Set([...(Array.isArray(parsed) ? parsed.filter((value): value is string => typeof value === "string") : []), ...memory]);
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
	if (onboardingWindowOpen() && RUN_PAGES.has(step)) run.step = step;
	saveRun(run);
}

/**
 * Reports a step phase. `extra` carries the step's own closed properties
 * (what was missing, how much of the welcome story played, where the
 * project came from).
 */
export function reportOnboardingStep(step: OnboardingStep, phase: StepPhase, extra: Record<string, unknown> = {}): void {
	// Abandoned is only reported as the window closes.
	reportStep(step, phase, extra, { onUnload: phase === "abandoned" });
}

/**
 * `onUnload`: the window is closing, so the event is sent at once rather than
 * queued. `shown: false`: a view whose showing was already noted (by
 * useOnboardingStep when the step opened), so its time on step runs from then.
 */
function reportStep(step: OnboardingStep, phase: StepPhase, extra: Record<string, unknown>, options: { onUnload?: boolean; shown?: boolean } = {}): void {
	if (!tracking()) return;
	if (phase === "viewed" && options.shown !== false) noteStepShown(step);
	if (!reserveOnboardingPhase(step, phase)) return;
	const timing = phase === "viewed" ? {} : { time_on_step_ms: timeOnStep(step) };
	const properties = { step, ...extra, ...timing };
	if (options.onUnload) captureOnUnload(`ao.onboarding.step_${phase}`, properties);
	else capture(`ao.onboarding.step_${phase}`, properties);
}

/** Reports a step's view unless it already has one, so a step that finishes the moment it starts still reads viewed, then completed. */
export function ensureOnboardingStepViewed(step: OnboardingStep, extra: Record<string, unknown> = {}): void {
	if (readReported().has(`${step}:viewed`)) return;
	// A step useOnboardingStep already noted as shown keeps its start time.
	reportStep(step, "viewed", extra, { shown: !stepStartedAt.has(step) });
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
export function skipOnboardingStep(step: OnboardingStep, reason: OnboardingSkipReason, extra: Record<string, unknown> = {}): void {
	if (!tracking()) return;
	if (reason === "already_done") {
		if (!reserve(`${step}:skipped`)) return;
		const run = currentRun();
		if (run && !run.completedAt) {
			run.autoSkipped += 1;
			saveRun(run);
		}
		capture("ao.onboarding.step_skipped", { step, reason, ...extra });
		return;
	}
	capture("ao.onboarding.step_skipped", { step, reason, ...extra, time_on_step_ms: timeOnStep(step) });
}

/** Back, from one page to an earlier one. Sent per click. */
export function reportOnboardingBack(from: OnboardingStep, to: OnboardingStep): void {
	if (!tracking()) return;
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
 * can be left by Back, which reports its own completion. `viewReady: false`
 * holds back only the view (until its `viewed` properties are known); a close
 * before then still reports the view, with what is known, and the abandon.
 */
export function useOnboardingStep(
	step: OnboardingStep,
	active: boolean,
	options: { autoComplete?: boolean; viewed?: Record<string, unknown>; viewReady?: boolean } = {},
): (extra?: Record<string, unknown>) => void {
	const wasActive = useRef(false);
	const autoComplete = options.autoComplete ?? true;
	const viewReady = options.viewReady ?? true;
	const viewed = useRef(options.viewed);
	viewed.current = options.viewed;
	useEffect(() => {
		if (!active) {
			if (wasActive.current && autoComplete) reportOnboardingStep(step, "completed");
			wasActive.current = false;
			return;
		}
		wasActive.current = true;
		// Shown from now, even while its view waits for viewReady.
		if (tracking()) noteStepShown(step);
		const onHide = () => {
			// Closed before the view was sent: send it with what is known, then the abandon.
			if (!readReported().has(`${step}:viewed`)) reportStep(step, "viewed", viewed.current ?? {}, { onUnload: true, shown: false });
			reportOnboardingStep(step, "abandoned");
		};
		window.addEventListener("pagehide", onHide);
		return () => window.removeEventListener("pagehide", onHide);
	}, [step, active, autoComplete]);
	useEffect(() => {
		if (active && viewReady) reportStep(step, "viewed", viewed.current ?? {}, { shown: false });
	}, [step, active, viewReady]);
	return useCallback((extra?: Record<string, unknown>) => reportOnboardingStep(step, "completed", extra), [step]);
}

// ── The run ─────────────────────────────────────────────────────────────────

function abandonRun() {
	if (!onboardingWindowOpen() || abandonedThisLaunch || !tracking()) return;
	const run = currentRun();
	if (!run || run.completedAt) return;
	abandonedThisLaunch = true;
	run.lastSeenAt = Date.now();
	saveRun(run);
	captureOnUnload("ao.onboarding.abandoned", { step: run.step, duration_ms: Date.now() - run.startedAt, launch_count: run.launches });
}

/**
 * The onboarding window opened on `step`. A first run is started once per
 * install and resumed on every later launch that reopens it; a replay starts
 * a new run each time. Returns the cleanup for when the window closes.
 */
export function beginOnboardingRun(kind: OnboardingRunKind, step: OnboardingStep): () => void {
	const now = Date.now();
	setOnboardingWindowOpen(true);
	if (!tracking()) {
		// Not counted while opted out; a later launch that reopens it starts the run then.
		return () => {
			setOnboardingWindowOpen(false);
		};
	}
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
		setOnboardingWindowOpen(false);
	};
}

/** The run ended: the last page's Continue (`finished`), Skip setup, or closed for the person (`superseded`). */
export function completeOnboardingRun(outcome: OnboardingOutcome): void {
	// Only a finished run goes on into the first-task funnel, which ends what is still running (FirstTaskFunnel).
	if (outcome !== "finished") leaveOnboardingActions();
	else noteOnboardingPagesFinished();
	if (!tracking()) {
		setOnboardingWindowOpen(false);
		return;
	}
	const run = currentRun();
	if (!run || run.completedAt) return;
	const now = Date.now();
	run.completedAt = now;
	saveRun(run);
	setOnboardingWindowOpen(false);
	capture("ao.onboarding.completed", {
		step: run.step,
		outcome,
		duration_ms: now - run.startedAt,
		launch_count: run.launches,
		resumed: run.resumed === true,
		back_count: run.backCount,
		auto_skipped_count: run.autoSkipped,
	});
	// A finished replay goes on into its tour, which ends it (endOnboardingReplay).
	if (run === replay && outcome !== "finished") replay = null;
}

/** A replay's tour ended, so the replay has: what follows counts for the first run again. */
export function endOnboardingReplay(): void {
	if (replay?.completedAt) replay = null;
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

// ── Actions: installs and sign-ins ─────────────────────────────────────────

const attempts = new Map<string, number>();
const pending = new Map<string, { attempt: number; startedAt: number; step?: OnboardingStep }>();
/**
 * Attempts started during onboarding and not yet ended, each with an id from
 * this launch, kept in memory under either analytics choice: they decide what
 * reaches Sentry, and which check still belongs to the attempt in progress.
 */
const errorWatch = new Map<string, number>();
let watchIds = 0;

function actionKey(action: OnboardingAction, target?: string) {
	return `${action}:${target ?? ""}`;
}

/** Something the person (or the page, `auto`) started that can fail and be retried. Attempts count per launch. */
export function startOnboardingAction(
	action: OnboardingAction,
	options: { target?: string; step?: OnboardingStep; auto?: boolean } = {},
): void {
	const key = actionKey(action, options.target);
	errorWatch.set(key, ++watchIds);
	if (!tracking()) return;
	const attempt = (attempts.get(key) ?? 0) + 1;
	attempts.set(key, attempt);
	pending.set(key, { attempt, startedAt: Date.now(), step: options.step });
	capture("ao.onboarding.action_started", { action, target: options.target, step: options.step, attempt, auto: options.auto });
}

/** Ends the attempt in progress for (action, target), once; nothing when none is. Returns whether it reported. */
export function finishOnboardingAction(
	action: OnboardingAction,
	outcome: OnboardingActionOutcome,
	options: { target?: string; reason?: OnboardingFailureReason; errorCode?: string; attempt?: number; onUnload?: boolean } = {},
): boolean {
	const key = actionKey(action, options.target);
	const watched = errorWatch.get(key);
	// A check that outlived its attempt (a retry started meanwhile) leaves the new one alone.
	if (watched !== undefined && options.attempt !== undefined && options.attempt !== watched) return false;
	const attempt = pending.get(key);
	// Once per attempt, and only for one started during onboarding: the same
	// panels outside onboarding (the clone dialog's GitHub sign-in) send nothing.
	if (errorWatch.delete(key) && outcome === "failed" && options.reason) captureOnboardingFailureToSentry(action, options.reason, options.target);
	if (!tracking() || !attempt) return false;
	pending.delete(key);
	(options.onUnload ? captureOnUnload : capture)("ao.onboarding.action_finished", {
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

/**
 * Onboarding ended (Skip setup, closed for the person, the tour ended) or the
 * window is closing: every attempt still running ends as `left`, since no page
 * is left to see how it ends.
 */
export function leaveOnboardingActions(options: { onUnload?: boolean } = {}): void {
	for (const key of [...errorWatch.keys()]) {
		const split = key.indexOf(":");
		const target = key.slice(split + 1);
		finishOnboardingAction(key.slice(0, split) as OnboardingAction, "left", { target: target || undefined, onUnload: options.onUnload });
	}
}

// The window closing mid-onboarding (the first-run window or a tour step).
window.addEventListener("pagehide", () => {
	if (onboardingInProgress()) leaveOnboardingActions({ onUnload: true });
});

/** Whether an action started during onboarding has not ended yet, under either analytics choice. */
export function onboardingActionWatched(action: OnboardingAction, target?: string): boolean {
	return errorWatch.has(actionKey(action, target));
}

/**
 * The attempt in progress for (action, target), to pass back to
 * finishOnboardingAction from an async check; under either analytics choice.
 * An id for this launch, not the `attempt` count sent with the events.
 */
export function onboardingActionAttempt(action: OnboardingAction, target?: string): number | undefined {
	return errorWatch.get(actionKey(action, target));
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

// ── The first-task funnel's project step ────────────────────────────────────

let projectNote: { source: "clone" | "local_folder"; projectId: string; at: number } | undefined;

/**
 * A project was cloned or added; the tour's project step reads where it came
 * from (project_source on its completion). Its success and failure are already
 * ao.projects.added / add_failed / clone_failed, so no action is sent here.
 */
export function reportOnboardingProjectAdded(source: "clone" | "local_folder", projectId: string): void {
	if (!tracking()) return;
	projectNote = { source, projectId, at: Date.now() };
}

/** Where the project that ended the tour's project step came from; only a note for that same project counts. */
export function onboardingProjectSource(projectId: string | undefined, isNew: boolean): OnboardingProjectSource {
	const note = projectNote;
	if (note && note.projectId === projectId) {
		projectNote = undefined;
		if (Date.now() - note.at < PROJECT_NOTE_TTL_MS) return note.source;
	}
	return isNew ? "other" : "existing";
}

// ── Activation ──────────────────────────────────────────────────────────────

/** The first chat message delivered after a first run finished, within a week, once per install. */
export function recordOnboardingActivation(duringTour: boolean, now = Date.now()): void {
	if (replay || !tracking()) return;
	const run = readFirstRun();
	if (!run?.completedAt || run.activated) return;
	if (now - run.completedAt > ACTIVATION_WINDOW_MS) return;
	run.activated = true;
	saveRun(run);
	void captureRendererEvent("ao.onboarding.activated", {
		milestone: "first_message",
		time_since_completed_ms: now - run.completedAt,
		during_tour: duringTour,
		// Only the unguided funnel sends activation now (FirstTaskFunnel).
		funnel_variant: "unguided",
	});
}

/** Listens for the first delivered chat message for activation. Mounted once, by FirstTaskFunnel. */
export function useOnboardingActivation(duringTour: () => boolean): void {
	const duringTourRef = useRef(duringTour);
	duringTourRef.current = duringTour;
	useEffect(() => onComposerSent(() => recordOnboardingActivation(duringTourRef.current())), []);
}

/**
 * Drops what would otherwise be counted into a later event: the run (so a
 * completion after an opt-in does not carry the opted-out period, or arrive
 * without a start), attempts, pending actions and step timings. The step
 * slots already reported stay, so an opt-in does not report them twice.
 */
function forgetOnOptOut() {
	memoryRun = null;
	storedRunDiscarded = true;
	try {
		storage()?.removeItem(RUN_KEY);
	} catch {
		// Memory is cleared; capture is gated on the opt-out anyway.
	}
	replay = null;
	abandonedThisLaunch = false;
	stepStartedAt.clear();
	attempts.clear();
	pending.clear();
	projectNote = undefined;
}

onAnalyticsOptedOut(forgetOnOptOut);

/** Test seam: forgets this launch's in-memory state. */
export function resetOnboardingTelemetryForTests(): void {
	memory.clear();
	memoryRun = null;
	storedRunDiscarded = false;
	replay = null;
	abandonedThisLaunch = false;
	stepStartedAt.clear();
	attempts.clear();
	pending.clear();
	errorWatch.clear();
	projectNote = undefined;
	resetOnboardingFlowForTests();
}
