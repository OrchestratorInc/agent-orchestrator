import { hostedProjectKey } from "./hostedRows";
import type { MachineAppearance } from "./machineAppearance";

export const ALL_WORKER_PROJECTS = "all";
export const ALL_WORKER_ENVIRONMENTS = "all";

type WorkerProject = { id: string; name: string; hostId?: string; hostName?: string };
export type WorkerEnvironment = { id: string; name: string; appearance?: MachineAppearance };

export function spawnProjectParam(projectId: string, projects?: readonly WorkerProject[]): { projectId: string; hostId?: string } | undefined {
	if (projectId === ALL_WORKER_PROJECTS) return undefined;
	if (!projects) return { projectId };
	const project = projects.find((item) => hostedProjectKey(item) === projectId);
	return project ? { projectId: project.id, ...(project.hostId ? { hostId: project.hostId } : {}) } : undefined;
}

export function filterWorkersByProject<T extends { projectId: string; hostId?: string }>(
	workers: readonly T[],
	projectId: string,
	projects: readonly WorkerProject[] = [],
): T[] {
	if (projectId === ALL_WORKER_PROJECTS) return [...workers];
	const project = projects.find((item) => hostedProjectKey(item) === projectId);
	if (project?.hostId) return workers.filter((worker) => worker.hostId === project.hostId && worker.projectId === project.id);
	return workers.filter((worker) => worker.projectId === projectId);
}

export function filterWorkersByEnvironmentAndProject<T extends { projectId: string; hostId?: string }>(
	workers: readonly T[],
	environmentId: string,
	projectId: string,
	projects: readonly WorkerProject[] = [],
): T[] {
	const environmentWorkers = environmentId === ALL_WORKER_ENVIRONMENTS
		? workers
		: workers.filter((worker) => worker.hostId === environmentId);
	return filterWorkersByProject(environmentWorkers, projectId, projects);
}

export function workerSearchPresentation(
	requestedOpen: boolean,
	query: string,
): "collapsed" | "expanded" {
	return requestedOpen || query.trim().length > 0 ? "expanded" : "collapsed";
}

export function workerProjectLabel(
	projects: readonly WorkerProject[],
	projectId: string,
	environmentId: string = ALL_WORKER_ENVIRONMENTS,
): string {
	if (projectId === ALL_WORKER_PROJECTS) return "All projects";
	return workerProjectOptions(projects, environmentId).find((project) => project.id === projectId)?.label ?? "All projects";
}

export function workerProjectOptions(
	projects: readonly WorkerProject[],
	environmentId: string = ALL_WORKER_ENVIRONMENTS,
): { id: string; label: string }[] {
	const visibleProjects = environmentId === ALL_WORKER_ENVIRONMENTS
		? projects
		: projects.filter((project) => project.hostId === environmentId);
	const multipleHosts = new Set(visibleProjects.map((project) => project.hostId).filter(Boolean)).size > 1;
	return [
		{ id: ALL_WORKER_PROJECTS, label: "All projects" },
		...visibleProjects.map((project) => ({
			id: hostedProjectKey(project),
			label: multipleHosts && project.hostName ? `${project.name} · ${project.hostName}` : project.name,
		})),
	];
}

export function workerProjectForEnvironment(
	projects: readonly WorkerProject[],
	environmentId: string,
	projectId: string,
): string {
	return workerProjectOptions(projects, environmentId).some((project) => project.id === projectId)
		? projectId
		: ALL_WORKER_PROJECTS;
}

export function workerEnvironmentOptions(
	environments: readonly WorkerEnvironment[],
): { id: string; label: string; appearance?: MachineAppearance }[] {
	return [
		{ id: ALL_WORKER_ENVIRONMENTS, label: "All environments" },
		...environments.map((environment) => ({ id: environment.id, label: environment.name, ...(environment.appearance ? { appearance: environment.appearance } : {}) })),
	];
}

/** Machines in name order, so the filter keeps its order across launches and polls. */
export function sortWorkerEnvironments<T extends WorkerEnvironment>(environments: readonly T[]): T[] {
	return [...environments].sort((a, b) =>
		a.name.toLocaleLowerCase().localeCompare(b.name.toLocaleLowerCase()) || a.id.localeCompare(b.id),
	);
}

/** One machine leaves nothing to filter, so the chip row only appears from two. */
export function showsEnvironmentFilter(environments: readonly WorkerEnvironment[]): boolean {
	return environments.length > 1;
}

/**
 * Where a horizontal chip row must scroll so a chip is fully visible with `inset`
 * of breathing room, or null when it already is.
 */
export function chipRevealOffset(chip: { x: number; width: number }, view: { offset: number; width: number; inset: number }): number | null {
	if (chip.x - view.inset < view.offset) return Math.max(0, chip.x - view.inset);
	const end = chip.x + chip.width + view.inset;
	if (end > view.offset + view.width) return end - view.width;
	return null;
}
