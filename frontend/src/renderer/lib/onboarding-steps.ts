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
 * - tour_project, tour_orchestrator, first_task: after the pages, the first
 *   project, its orchestrator chat and the first message (FirstTaskFunnel;
 *   named for the guided tour that used to walk through them)
 * - finish: the guided tour's closing note (not sent since the tour was removed)
 * - work_defaults: what new projects start with (their agent, and how agents
 *   finish their work), the last page
 *   (appended, so its index comes after the steps that follow it in the flow)
 * - default_agents: the agents page that sat between Your tools and
 *   work_defaults (not sent since the agent moved onto work_defaults)
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
	"work_defaults",
	"default_agents",
] as const;
export type OnboardingStep = (typeof ONBOARDING_STEPS)[number];

export function onboardingStepIndex(step: OnboardingStep): number {
	return ONBOARDING_STEPS.indexOf(step);
}
