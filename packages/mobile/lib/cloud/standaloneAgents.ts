import type { CloudOffering, CloudProject } from "./types";

// A Cloud standalone agent is one worker with no repository and no
// orchestrator, its owner's own. The control plane backs each with a hidden
// project of its own (standaloneAgent), which the app keeps so lookups by
// project id still work, but never lists as a project: its session reads as
// a standalone session, as a machine's does.

/** The offering feature a control plane that starts standalone agents lists. */
export const STANDALONE_AGENTS_FEATURE = "standalone_agents";

export function standaloneAgentsOffered(offering: CloudOffering | undefined): boolean {
	return offering?.features?.includes(STANDALONE_AGENTS_FEATURE) === true;
}

/**
 * The projects and the agents, read apart so many agents can't page the
 * projects out, as one list. A control plane without agents ignores the
 * filter and lists every project in both reads; each is kept once.
 */
export function mergeCloudProjectLists(projects: readonly CloudProject[], agents: readonly CloudProject[]): CloudProject[] {
	const listed = new Set(projects.map((project) => project.id));
	return [...projects, ...agents.filter((project) => !listed.has(project.id))];
}

/** Put a project the phone just created into the list without waiting for the next read. */
export function upsertCloudProject(projects: readonly CloudProject[], project: CloudProject): CloudProject[] {
	return projects.some((item) => item.id === project.id)
		? projects.map((item) => (item.id === project.id ? project : item))
		: [...projects, project];
}
