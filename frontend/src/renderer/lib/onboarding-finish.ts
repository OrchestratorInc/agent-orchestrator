import { STANDALONE_WORKSPACE_ID } from "../types/workspace";
import type { OnboardingFinishRequest } from "../stores/ui-store";
import { rememberTaskComposerPreference } from "./task-composer-preferences";

export const ONBOARDING_COMPLETE_STORAGE_KEY = "ao.onboarding.completed";

export function markOnboardingComplete(): void {
	try {
		window.localStorage.setItem(ONBOARDING_COMPLETE_STORAGE_KEY, "1");
	} catch {
		// Storage can be unavailable (private mode, locked profile). Onboarding
		// still completed; the flag only decides whether to show it again.
	}
}

export function hasCompletedOnboarding(): boolean {
	try {
		return window.localStorage.getItem(ONBOARDING_COMPLETE_STORAGE_KEY) !== null;
	} catch {
		return false;
	}
}

export type OnboardingFinishDeps = {
	createProject: (input: {
		asWorkspace?: boolean;
		clonePreparationId?: string;
		orchestratorAgent: string;
		path: string;
		workerAgent: string;
	}) => Promise<unknown>;
};

export type OnboardingFinishOutcome = { ok: true; standalone?: boolean } | { ok: false; message: string };

/**
 * Runs the last onboarding step: register the chosen project and start its
 * orchestrator. Onboarding is only marked complete when this succeeds, so a
 * failure leaves the user in the flow with their choices intact instead of
 * dropping them on an empty board they cannot get back from.
 */
export async function runOnboardingFinish(
	request: OnboardingFinishRequest,
	deps: OnboardingFinishDeps,
): Promise<OnboardingFinishOutcome> {
	if (request.standalone) {
		// No project to register. The Scratchpad composer opens on the agent they
		// picked, since it preselects the last agent used in that context.
		rememberTaskComposerPreference(STANDALONE_WORKSPACE_ID, request.orchestratorAgent, { mode: "", model: "" });
		markOnboardingComplete();
		return { ok: true, standalone: true };
	}
	try {
		await deps.createProject({
			asWorkspace: request.asWorkspace,
			clonePreparationId: request.clonePreparationId,
			orchestratorAgent: request.orchestratorAgent,
			path: request.path,
			workerAgent: request.workerAgent,
		});
		markOnboardingComplete();
		return { ok: true };
	} catch (error) {
		return { message: error instanceof Error ? error.message : "", ok: false };
	}
}
