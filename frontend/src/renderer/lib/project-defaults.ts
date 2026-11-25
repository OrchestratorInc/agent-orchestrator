import type { components } from "../../api/schema";
import type { ProjectAgentDefaults } from "./agent-select-options";

// What each project added from here starts with, chosen once during
// onboarding: its orchestrator and worker agents (the agents page, applied
// where new projects pick their agents, loadProjectAgentDefaults) and how
// agents finish their work (the last page, written into each project's config
// as it is added, createProjectConfig). They are ordinary project settings
// from then on: agents and review in the project's settings, archive-on-merge
// and the auto-fix switches per chat, issue intake on the Add Project sheet and
// in project settings. Nothing changes for projects added before.
//
// A per-viewer convenience kept in renderer storage, like the onboarding's
// other resume state: without it, projects start on the daemon's defaults.

export type ProjectDefaults = {
	/** Archive a chat (terminate its session) once its pull request merges. */
	archiveOnMerge: boolean;
	/** A second agent reviews each pull request. */
	review: boolean;
	/** The agent that reviews, when `review` is on. */
	reviewerAgent?: string;
	/** Start the Add Project sheet's issue intake switched on. */
	assignedIssues: boolean;
	/** Send failing checks and merge conflicts to the chat's agent to fix (one
	 * daemon switch covers both). */
	fixChecks: boolean;
	/** Send PR review comments to the chat's agent to fix. */
	fixReviewComments: boolean;
	/** The agent new projects run their orchestrator on, when ready. */
	orchestratorAgent?: string;
	/** The agent new projects run their workers on, when ready. */
	workerAgent?: string;
};

/** What onboarding's page shows before the person changes anything. */
export const INITIAL_PROJECT_DEFAULTS: ProjectDefaults = { archiveOnMerge: true, review: true, assignedIssues: false, fixChecks: true, fixReviewComments: true };

const agentId = (value: unknown) => (typeof value === "string" && value ? value : undefined);

const STORAGE_KEY = "ao.projectDefaults";

export function readProjectDefaults(): ProjectDefaults | null {
	try {
		const parsed = JSON.parse(window.localStorage.getItem(STORAGE_KEY) ?? "null") as Partial<ProjectDefaults> | null;
		if (!parsed || typeof parsed !== "object") return null;
		return {
			archiveOnMerge: parsed.archiveOnMerge === true,
			review: parsed.review === true,
			reviewerAgent: agentId(parsed.reviewerAgent),
			assignedIssues: parsed.assignedIssues === true,
			// On unless turned off: the daemon's own default.
			fixChecks: parsed.fixChecks !== false,
			fixReviewComments: parsed.fixReviewComments !== false,
			orchestratorAgent: agentId(parsed.orchestratorAgent),
			workerAgent: agentId(parsed.workerAgent),
		};
	} catch {
		return null;
	}
}

/** Saves some of the choices, keeping the rest (each onboarding page saves its own). */
export function saveProjectDefaults(defaults: Partial<ProjectDefaults>): void {
	try {
		window.localStorage.setItem(STORAGE_KEY, JSON.stringify({ ...(readProjectDefaults() ?? INITIAL_PROJECT_DEFAULTS), ...defaults }));
	} catch {
		// Projects then start on the daemon's defaults.
	}
}

type ProjectConfig = components["schemas"]["ProjectConfig"];

/** The project config the choices stand for. Review without a known agent
 * leaves the reviewer to the project's own default. */
export function projectDefaultsConfig(defaults: ProjectDefaults | null, fallbackReviewer?: string): Partial<ProjectConfig> {
	if (!defaults) return {};
	const reviewer = defaults.reviewerAgent ?? fallbackReviewer;
	return {
		terminateOnPrMerge: defaults.archiveOnMerge,
		autoInjectCI: defaults.fixChecks,
		autoInjectReview: defaults.fixReviewComments,
		...(defaults.review ? { autoReview: true, ...(reviewer ? { reviewers: [{ harness: reviewer }] } : {}) } : {}),
	};
}

/** The agents a new project starts on: the ones chosen during onboarding
 * while they are still ready here, otherwise the ranked pick. */
export function withChosenAgents(
	ranked: ProjectAgentDefaults,
	agents: readonly { id: string; effectiveReadiness?: string }[],
	chosen: ProjectDefaults | null,
): ProjectAgentDefaults {
	const ready = (id: string | undefined) => (id && agents.some((agent) => agent.id === id && agent.effectiveReadiness === "ready") ? id : undefined);
	return {
		...ranked,
		orchestratorAgent: ready(chosen?.orchestratorAgent) ?? ranked.orchestratorAgent,
		workerAgent: ready(chosen?.workerAgent) ?? ranked.workerAgent,
	};
}
