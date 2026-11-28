import type { OnboardingStep } from "./onboarding-steps";

// Whether onboarding is showing now: the first-run window or a first-task funnel
// step. Kept apart from the funnel (onboarding-telemetry.ts) so Sentry reads
// it directly on each capture. Nothing registers on import, so a reloaded
// copy of the funnel module (hot reload, tests) can't leave Sentry reading
// another copy's state.

let windowOpen = false;
let tourStep: OnboardingStep | undefined;
/** Onboarding's pages finished in this launch, so the tour showing now follows them. */
let finishedThisLaunch = false;

export function setOnboardingWindowOpen(open: boolean): void {
	windowOpen = open;
}

export function onboardingWindowOpen(): boolean {
	return windowOpen;
}

/** The first-task funnel step running now, or undefined; set by FirstTaskFunnel. */
export function setOnboardingTourStep(step: OnboardingStep | undefined): void {
	tourStep = step;
}

/** The last page's Continue, which goes on into the tour. */
export function noteOnboardingPagesFinished(): void {
	finishedThisLaunch = true;
}

/**
 * Whether onboarding is showing now: the first-run window or a tour step.
 * Setup that can also be reached later (the clone dialog's GitHub panel)
 * reports to the funnel only then.
 */
export function onboardingInProgress(): boolean {
	return windowOpen || tourStep !== undefined;
}

/**
 * The flow Sentry tags renderer events with: the first-run window, or a tour
 * step in the launch whose pages finished. A tour restored on a later launch
 * stays until it is dismissed, for days or weeks if the card is ignored, so it
 * does not tag those launches' errors.
 */
export function onboardingSentryFlow(): "onboarding" | undefined {
	return windowOpen || (tourStep !== undefined && finishedThisLaunch) ? "onboarding" : undefined;
}

/** Test seam: forgets this launch's state. */
export function resetOnboardingFlowForTests(): void {
	windowOpen = false;
	tourStep = undefined;
	finishedThisLaunch = false;
}
