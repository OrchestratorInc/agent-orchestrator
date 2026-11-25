/**
 * The desktop onboarding funnel's steps, in the order a new user meets them.
 * A step's position here is its `step_index` on every onboarding event, so
 * append new steps where they sit in the flow and never reorder shipped ones
 * without noting it in docs/telemetry.md ("Desktop onboarding funnel").
 *
 * - dependencies: the blocking startup dialog for git, tmux or a coding agent
 * - welcome: the first page and its three-part story (the last part is CI/CD)
 * - sign_in: the optional AO Cloud account page
 * - tools: Your tools, the GitHub CLI and the coding agents
 * - github_auth: signing the GitHub CLI in, on Your tools
 * - import_chats: importing earlier chats (not reachable since #7767)
 * - tour_project, tour_orchestrator, first_task, finish: the guided tour
 */
export const ONBOARDING_STEPS = [
	"dependencies",
	"welcome",
	"sign_in",
	"tools",
	"github_auth",
	"import_chats",
	"tour_project",
	"tour_orchestrator",
	"first_task",
	"finish",
] as const;
export type OnboardingStep = (typeof ONBOARDING_STEPS)[number];

export function onboardingStepIndex(step: OnboardingStep): number {
	return ONBOARDING_STEPS.indexOf(step);
}
