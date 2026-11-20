// The onboarding actions and failure reasons, shared by the renderer's funnel
// schema and Electron main's Sentry allowlist so both read one closed list.

/** Things a person starts during onboarding that can fail and be retried. */
export const ONBOARDING_ACTIONS = [
	"dependency_install",
	"gh_install",
	"gh_sign_in",
	"agent_install",
	"agent_sign_in",
	"cloud_sign_in",
] as const;
export type OnboardingAction = (typeof ONBOARDING_ACTIONS)[number];

/**
 * Why an action did not succeed:
 * - start_failed: the daemon refused to start it
 * - install_failed / interrupted / unsupported: the install job's own end state
 * - lost_contact: the daemon stopped answering while it ran
 * - not_signed_in: the login ended and the check still says signed out
 * - cleanup_failed: a login's terminal could not be closed
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
	"request_failed",
	"documentation",
	"other",
] as const;
export type OnboardingFailureReason = (typeof ONBOARDING_FAILURE_REASONS)[number];

/**
 * Failures that create a Sentry issue: something broke. A login the person
 * did not finish (`not_signed_in`), a platform without an installer
 * (`unsupported`) and an agent that signs in outside AO (`documentation`) are
 * setup states, counted only in PostHog.
 */
export const ONBOARDING_SENTRY_REASONS: ReadonlySet<OnboardingFailureReason> = new Set([
	"start_failed",
	"install_failed",
	"interrupted",
	"lost_contact",
	"cleanup_failed",
	"request_failed",
	"other",
]);

const ACTIONS: ReadonlySet<string> = new Set(ONBOARDING_ACTIONS);

/** The Sentry `code` for a failed onboarding action, `action:reason`. */
export function onboardingFailureCode(action: OnboardingAction, reason: OnboardingFailureReason): string {
	return `${action}:${reason}`;
}

/** The action and reason in a Sentry `code`, or undefined when it is not one Sentry reports. */
export function parseOnboardingFailureCode(code: unknown): { action: OnboardingAction; reason: OnboardingFailureReason } | undefined {
	if (typeof code !== "string") return undefined;
	const [action, reason, extra] = code.split(":");
	if (extra !== undefined || !ACTIONS.has(action) || !ONBOARDING_SENTRY_REASONS.has(reason as OnboardingFailureReason)) return undefined;
	return { action: action as OnboardingAction, reason: reason as OnboardingFailureReason };
}
