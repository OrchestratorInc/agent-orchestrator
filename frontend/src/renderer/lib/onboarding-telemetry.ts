import { useCallback, useEffect, useRef } from "react";
import type { OnboardingStep } from "./onboarding-steps";
import { captureRendererEvent } from "./telemetry";

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

export function reserveOnboardingPhase(step: OnboardingStep, phase: OnboardingPhase): boolean {
	const reported = readReported();
	if (phase === "abandoned" && reported.has(`${step}:completed`)) return false;
	const key = `${step}:${phase}`;
	if (reported.has(key)) return false;
	reported.add(key);
	memory.add(key);
	try {
		window.localStorage.setItem(STORAGE_KEY, JSON.stringify([...reported]));
	} catch {
	}
	return true;
}

export function reportOnboardingStep(step: OnboardingStep, phase: OnboardingPhase): void {
	if (!reserveOnboardingPhase(step, phase)) return;
	void captureRendererEvent(`ao.onboarding.step_${phase}`, { step });
}

export function completeOnboardingStepIfViewed(step: OnboardingStep): void {
	if (!readReported().has(`${step}:viewed`)) return;
	reportOnboardingStep(step, "completed");
}

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
