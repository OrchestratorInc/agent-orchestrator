import { ONBOARDING_STEPS, onboardingStepIndex, type OnboardingStep } from "./onboarding-steps";

// The onboarding funnel's closed vocabulary and its per-event property
// allowlist. telemetry.ts hands every ao.onboarding.* event here, so a value
// outside these lists never reaches PostHog whatever a call site passes. Only
// enums, booleans, small counts, buckets and durations are kept: never a path,
// repository name or URL, username, email or error message.
//
// Pure (no imports from telemetry.ts) so telemetry.ts can import it.

export const ONBOARDING_RUN_KINDS = ["first_run", "replay"] as const;
export type OnboardingRunKind = (typeof ONBOARDING_RUN_KINDS)[number];

/** How a run ended: the last page's Continue, Skip setup, or closed for the person (a project, or an offer to migrate an older installation, appeared). */
export const ONBOARDING_OUTCOMES = ["finished", "skipped_setup", "superseded"] as const;
export type OnboardingOutcome = (typeof ONBOARDING_OUTCOMES)[number];

/** already_done: passed over because there was nothing to do; user: the person moved on without doing it; dismissed: the tour was closed. */
export const ONBOARDING_SKIP_REASONS = ["already_done", "user", "dismissed"] as const;
export type OnboardingSkipReason = (typeof ONBOARDING_SKIP_REASONS)[number];

/** Things a person starts during onboarding that can fail and be retried. */
export const ONBOARDING_ACTIONS = [
	"dependency_install",
	"gh_install",
	"gh_sign_in",
	"agent_install",
	"agent_sign_in",
	"cloud_sign_in",
	"project_clone",
	"project_add",
] as const;
export type OnboardingAction = (typeof ONBOARDING_ACTIONS)[number];

export const ONBOARDING_ACTION_OUTCOMES = ["succeeded", "failed", "cancelled", "timed_out", "handed_off"] as const;
export type OnboardingActionOutcome = (typeof ONBOARDING_ACTION_OUTCOMES)[number];

/**
 * Why an action did not succeed:
 * - start_failed: the daemon refused to start it
 * - install_failed / interrupted / unsupported: the install job's own end state
 * - lost_contact: the daemon stopped answering while it ran
 * - not_signed_in: the login ended and the check still says signed out
 * - cleanup_failed: a login's terminal could not be closed
 * - refused: the daemon refused a project clone or add (see error_code)
 * - request_failed: the request never got an answer
 * - documentation: the agent signs in outside AO, so its guide was opened
 */
export const ONBOARDING_FAILURE_REASONS = [
	"start_failed",
	"install_failed",
	"interrupted",
	"unsupported",
	"lost_contact",
	"not_signed_in",
	"cleanup_failed",
	"refused",
	"request_failed",
	"documentation",
	"other",
] as const;
export type OnboardingFailureReason = (typeof ONBOARDING_FAILURE_REASONS)[number];

/** What a page found missing: tools, and whether any agent is installed or signed in. */
export const ONBOARDING_MISSING = ["git", "tmux", "gh", "gh_auth", "agent", "agent_auth"] as const;
export type OnboardingMissing = (typeof ONBOARDING_MISSING)[number];

/** How the tour's project step was met: a clone, a local folder, a project that was already there, or anything else (a cloud project). */
export const ONBOARDING_PROJECT_SOURCES = ["clone", "local_folder", "existing", "other"] as const;
export type OnboardingProjectSource = (typeof ONBOARDING_PROJECT_SOURCES)[number];

export const ONBOARDING_ACTIVATION_MILESTONES = ["first_message"] as const;

export const ONBOARDING_EVENTS = new Set([
	"ao.onboarding.started",
	"ao.onboarding.resumed",
	"ao.onboarding.abandoned",
	"ao.onboarding.completed",
	"ao.onboarding.step_viewed",
	"ao.onboarding.step_completed",
	"ao.onboarding.step_abandoned",
	"ao.onboarding.step_skipped",
	"ao.onboarding.back_clicked",
	"ao.onboarding.action_started",
	"ao.onboarding.action_finished",
	"ao.onboarding.activated",
	// Not sent since #7767 removed the Home first-run prompt; kept so the
	// allowlist stays closed if they return.
	"ao.onboarding.first_run_experiment_exposed",
	"ao.onboarding.first_run_prompt_action",
]);

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;
/** Longer than any real onboarding; a clock jump is clamped rather than sent. */
const MAX_DURATION_MS = 30 * DAY;

/** Total onboarding time, and time away between launches. */
export function durationBucket(ms: number): string {
	if (ms < MINUTE) return "lt_1m";
	if (ms < 3 * MINUTE) return "1m_3m";
	if (ms < 10 * MINUTE) return "3m_10m";
	if (ms < 30 * MINUTE) return "10m_30m";
	if (ms < 2 * HOUR) return "30m_2h";
	if (ms < DAY) return "2h_1d";
	return "gte_1d";
}

/** Time from finishing onboarding to the first message. */
export function activationBucket(ms: number): string {
	if (ms < 5 * MINUTE) return "lt_5m";
	if (ms < 15 * MINUTE) return "5m_15m";
	if (ms < HOUR) return "15m_1h";
	if (ms < DAY) return "1h_1d";
	return "1d_7d";
}

export function countBucket(count: number): string {
	if (count <= 0) return "0";
	if (count === 1) return "1";
	if (count <= 5) return "2_5";
	if (count <= 20) return "6_20";
	return "21_plus";
}

/** Registry agent ids and install targets (claude-code, codex, gh, tmux). */
const TARGET_PATTERN = /^[a-z][a-z0-9-]{0,39}$/;
/** An API error code, never its message. */
const ERROR_CODE_PATTERN = /^[A-Za-z0-9_]{1,64}$/;

type Properties = Record<string, unknown>;

function oneOf<T extends string>(value: unknown, allowed: readonly T[]): T | undefined {
	return typeof value === "string" && (allowed as readonly string[]).includes(value) ? (value as T) : undefined;
}

function boundedInt(value: unknown, min: number, max: number): number | undefined {
	return typeof value === "number" && Number.isInteger(value) && value >= min && value <= max ? value : undefined;
}

function duration(value: unknown): number | undefined {
	if (typeof value !== "number" || !Number.isFinite(value) || value < 0) return undefined;
	return Math.min(Math.round(value), MAX_DURATION_MS);
}

function missingList(value: unknown): string | undefined {
	if (typeof value !== "string") return undefined;
	const given = new Set(value.split(","));
	const kept = ONBOARDING_MISSING.filter((item) => given.has(item));
	return kept.length ? kept.join(",") : undefined;
}

function set(safe: Properties, key: string, value: unknown) {
	if (value !== undefined) safe[key] = value;
}

/** The step and its index; the index is derived, never taken from the caller. */
function stepFields(safe: Properties, properties: Properties | undefined) {
	const step = oneOf<OnboardingStep>(properties?.step, ONBOARDING_STEPS);
	if (!step) return;
	safe.step = step;
	safe.step_index = onboardingStepIndex(step);
}

export function sanitizeOnboardingProperties(event: string, properties?: Properties): Properties {
	const safe: Properties = {};
	const p = properties;
	switch (event) {
		case "ao.onboarding.first_run_experiment_exposed":
			if (p?.variant === "prompt" || p?.variant === "control") safe.variant = p.variant;
			return safe;
		case "ao.onboarding.first_run_prompt_action":
			set(safe, "action", oneOf(p?.action, ["clone", "local_folder", "standalone", "import", "dismissed"]));
			return safe;
		case "ao.onboarding.activated": {
			set(safe, "milestone", oneOf(p?.milestone, ONBOARDING_ACTIVATION_MILESTONES));
			const since = duration(p?.time_since_completed_ms);
			if (since !== undefined) {
				safe.time_since_completed_ms = since;
				safe.time_bucket = activationBucket(since);
			}
			if (typeof p?.during_tour === "boolean") safe.during_tour = p.during_tour;
			return safe;
		}
	}
	stepFields(safe, p);
	set(safe, "run_kind", oneOf(p?.run_kind, ONBOARDING_RUN_KINDS));
	switch (event) {
		case "ao.onboarding.step_viewed":
			set(safe, "missing", missingList(p?.missing));
			set(safe, "agents_ready", boundedInt(p?.agents_ready, 0, 100));
			if (typeof p?.chats_found === "number") safe.chats_found_bucket = countBucket(p.chats_found);
			break;
		case "ao.onboarding.step_completed":
			set(safe, "time_on_step_ms", duration(p?.time_on_step_ms));
			set(safe, "story_points_seen", boundedInt(p?.story_points_seen, 1, 3));
			if (typeof p?.story_completed === "boolean") safe.story_completed = p.story_completed;
			set(safe, "project_source", oneOf(p?.project_source, ONBOARDING_PROJECT_SOURCES));
			set(safe, "agents_ready", boundedInt(p?.agents_ready, 0, 100));
			if (typeof p?.gh_ready === "boolean") safe.gh_ready = p.gh_ready;
			break;
		case "ao.onboarding.step_abandoned":
			set(safe, "time_on_step_ms", duration(p?.time_on_step_ms));
			break;
		case "ao.onboarding.step_skipped":
			set(safe, "reason", oneOf(p?.reason, ONBOARDING_SKIP_REASONS));
			set(safe, "time_on_step_ms", duration(p?.time_on_step_ms));
			break;
		case "ao.onboarding.back_clicked":
			set(safe, "to_step", oneOf(p?.to_step, ONBOARDING_STEPS));
			break;
		case "ao.onboarding.started":
			break;
		case "ao.onboarding.resumed": {
			const away = duration(p?.away_ms);
			if (away !== undefined) {
				safe.away_ms = away;
				safe.away_bucket = durationBucket(away);
			}
			set(safe, "launch_count", boundedInt(p?.launch_count, 1, 1000));
			break;
		}
		case "ao.onboarding.abandoned":
		case "ao.onboarding.completed": {
			const total = duration(p?.duration_ms);
			if (total !== undefined) {
				safe.duration_ms = total;
				safe.duration_bucket = durationBucket(total);
			}
			set(safe, "launch_count", boundedInt(p?.launch_count, 1, 1000));
			if (event === "ao.onboarding.completed") {
				set(safe, "outcome", oneOf(p?.outcome, ONBOARDING_OUTCOMES));
				if (typeof p?.resumed === "boolean") safe.resumed = p.resumed;
				set(safe, "back_count", boundedInt(p?.back_count, 0, 1000));
				set(safe, "auto_skipped_count", boundedInt(p?.auto_skipped_count, 0, ONBOARDING_STEPS.length));
			}
			break;
		}
		case "ao.onboarding.action_started":
		case "ao.onboarding.action_finished":
			set(safe, "action", oneOf(p?.action, ONBOARDING_ACTIONS));
			if (typeof p?.target === "string" && TARGET_PATTERN.test(p.target)) safe.target = p.target;
			set(safe, "attempt", boundedInt(p?.attempt, 1, 100));
			if (event === "ao.onboarding.action_started") {
				if (typeof p?.auto === "boolean") safe.auto = p.auto;
				break;
			}
			set(safe, "outcome", oneOf(p?.outcome, ONBOARDING_ACTION_OUTCOMES));
			set(safe, "reason", oneOf(p?.reason, ONBOARDING_FAILURE_REASONS));
			if (typeof p?.error_code === "string" && ERROR_CODE_PATTERN.test(p.error_code)) safe.error_code = p.error_code;
			set(safe, "duration_ms", duration(p?.duration_ms));
			break;
	}
	return safe;
}

/**
 * The renderer's per-name rate limit (5 a minute) would drop the later steps
 * of someone clicking quickly through onboarding, so each onboarding event is
 * limited per step and action instead. Built from sanitized properties, so
 * the keys stay within the closed vocabulary. Undefined for other events.
 */
export function onboardingRateLimitKey(event: string, sanitized: Properties): string | undefined {
	if (!ONBOARDING_EVENTS.has(event)) return undefined;
	const parts = [event, sanitized.step, sanitized.action, sanitized.target].filter((part) => typeof part === "string");
	return parts.join(":");
}
