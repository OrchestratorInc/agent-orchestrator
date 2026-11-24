import type { components } from "../../api/schema";

// How agents finish their work, chosen once on onboarding's last page and
// written into each project's config as it is added (createProjectConfig).
// They are ordinary project settings from then on: review in the project's
// settings, archive-on-merge per chat, issue intake on the Add Project sheet
// and in project settings. Nothing changes for projects added before.
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
};

/** What onboarding's page shows before the person changes anything. */
export const INITIAL_PROJECT_DEFAULTS: ProjectDefaults = { archiveOnMerge: true, review: true, assignedIssues: false };

const STORAGE_KEY = "ao.projectDefaults";

export function readProjectDefaults(): ProjectDefaults | null {
	try {
		const parsed = JSON.parse(window.localStorage.getItem(STORAGE_KEY) ?? "null") as Partial<ProjectDefaults> | null;
		if (!parsed || typeof parsed !== "object") return null;
		return {
			archiveOnMerge: parsed.archiveOnMerge === true,
			review: parsed.review === true,
			reviewerAgent: typeof parsed.reviewerAgent === "string" && parsed.reviewerAgent ? parsed.reviewerAgent : undefined,
			assignedIssues: parsed.assignedIssues === true,
		};
	} catch {
		return null;
	}
}

export function saveProjectDefaults(defaults: ProjectDefaults): void {
	try {
		window.localStorage.setItem(STORAGE_KEY, JSON.stringify(defaults));
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
		...(defaults.review ? { autoReview: true, ...(reviewer ? { reviewers: [{ harness: reviewer }] } : {}) } : {}),
	};
}
