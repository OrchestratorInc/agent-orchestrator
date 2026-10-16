import { useCallback, useEffect, useRef } from "react";
import { captureRendererEvent } from "./telemetry";

// Desktop onboarding funnel: each step reports viewed, completed and abandoned at
// most once per install, so the counts read as a funnel rather than a page-view
// firehose. Closed vocabulary on both axes; nothing user-authored is sent.
export const ONBOARDING_STEPS = ["welcome", "github_auth"] as const;
export type OnboardingStep = (typeof ONBOARDING_STEPS)[number];
export type OnboardingPhase = "viewed" | "completed" | "abandoned";

const STORAGE_KEY = "ao.telemetry.onboardingSteps";
const memory = new Set<string>();

function readReported(): Set<string> {
	try {
		const parsed: unknown = JSON.parse(window.localStorage.getItem(STORAGE_KEY) ?? "[]");
		return new Set(Array.isArray(parsed) ? parsed.filter((v): v is string => typeof v === "string") : []);
	} catch {
		return memory;
	}
}

/** Reserves a (step, phase) slot; false when already reported or contradicted by an earlier phase. */
export function reserveOnboardingPhase(step: OnboardingStep, phase: OnboardingPhase): boolean {
	const reported = readReported();
	// A step that finished is never abandoned afterwards.
	if (phase === "abandoned" && reported.has(`${step}:completed`)) return false;
	const key = `${step}:${phase}`;
	if (reported.has(key)) return false;
	reported.add(key);
	memory.add(key);
	try {
		window.localStorage.setItem(STORAGE_KEY, JSON.stringify([...reported]));
	} catch {
		// Memory set above still dedupes within this launch.
	}
	return true;
}

export function reportOnboardingStep(step: OnboardingStep, phase: OnboardingPhase): void {
	if (!reserveOnboardingPhase(step, phase)) return;
	void captureRendererEvent(`ao.onboarding.step_${phase}`, { step });
}

/**
 * Reports a step as viewed while `active`, abandoned if the window closes while
 * it is still active, and completed when it goes from active to inactive. The
 * returned function reports completion explicitly for steps whose component
 * unmounts as part of finishing.
 */
export function useOnboardingStep(step: OnboardingStep, active: boolean): () => void {
	const wasActive = useRef(false);
	useEffect(() => {
		if (!active) {
			if (wasActive.current) reportOnboardingStep(step, "completed");
			wasActive.current = false;
			return;
		}
		wasActive.current = true;
		reportOnboardingStep(step, "viewed");
		const onHide = () => reportOnboardingStep(step, "abandoned");
		window.addEventListener("pagehide", onHide);
		return () => window.removeEventListener("pagehide", onHide);
	}, [step, active]);
	return useCallback(() => reportOnboardingStep(step, "completed"), [step]);
}
