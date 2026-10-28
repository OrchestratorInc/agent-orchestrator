import { STANDALONE_WORKSPACE_ID } from "../types/workspace";
import { agentLabel } from "./agent-options";
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
	/** Spawns a standalone worker on the chosen harness and resolves to its session id. */
	createStandaloneSession: (input: { displayName: string; harness: string }) => Promise<string>;
};

export type OnboardingFinishOutcome = { ok: true; sessionId?: string } | { ok: false; message: string };

/**
 * Runs the last onboarding step: register the chosen project and start its
 * orchestrator, or start a standalone agent when no project was chosen. Onboarding is only marked complete when this succeeds, so a
 * failure leaves the user in the flow with their choices intact instead of
 * dropping them on an empty board they cannot get back from.
 */
export async function runOnboardingFinish(
	request: OnboardingFinishRequest,
	deps: OnboardingFinishDeps,
): Promise<OnboardingFinishOutcome> {
	if (request.standalone) {
		// No project to register, and no prompt to wait for: the agent starts as
		// soon as onboarding ends. The composer preference still follows the pick,
		// so the next Scratchpad task preselects the same agent.
		try {
			const sessionId = await deps.createStandaloneSession({
				displayName: agentLabel(request.orchestratorAgent),
				harness: request.orchestratorAgent,
			});
			rememberTaskComposerPreference(STANDALONE_WORKSPACE_ID, request.orchestratorAgent, { mode: "", model: "" });
			markOnboardingComplete();
			return { ok: true, sessionId };
		} catch (error) {
			return { message: error instanceof Error ? error.message : "", ok: false };
		}
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
