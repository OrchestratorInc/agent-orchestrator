import type { DashboardSession, ProjectInfo } from "./api";
import { resourceKey, type Scoped } from "./environment/scopedBoard";
import type { EnvironmentKind } from "./environment/types";
import { hostedProjectKey } from "./hostedRows";

export const ALL_WORKER_PROJECTS = "all";

export const boardWorkerKey = (entry: Scoped<DashboardSession>): string => resourceKey(entry.source, entry.value.id);

export function scopedWorkerProjectOptions(projects: readonly Scoped<ProjectInfo>[], environment: "all" | EnvironmentKind) {
	return [
		{ id: ALL_WORKER_PROJECTS, label: "All projects" },
		...projects
			.filter((entry) => environment === "all" || entry.source.kind === environment)
			.map((entry) => ({
				id: resourceKey(entry.source, entry.value.id),
				label: `${entry.value.name} · ${entry.source.kind === "cloud" ? "Cloud" : "Local"}`,
			})),
	];
}

type WorkerProject = { id: string; name: string; hostId?: string; hostName?: string };

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

export function workerSearchPresentation(
	requestedOpen: boolean,
	query: string,
): "collapsed" | "expanded" {
	return requestedOpen || query.trim().length > 0 ? "expanded" : "collapsed";
}

export function workerProjectLabel(
	projects: readonly WorkerProject[],
	projectId: string,
): string {
	if (projectId === ALL_WORKER_PROJECTS) return "All projects";
	return workerProjectOptions(projects).find((project) => project.id === projectId)?.label ?? "All projects";
}

export function workerProjectOptions(
	projects: readonly WorkerProject[],
): { id: string; label: string }[] {
	const multipleHosts = new Set(projects.map((project) => project.hostId).filter(Boolean)).size > 1;
	return [
		{ id: ALL_WORKER_PROJECTS, label: "All projects" },
		...projects.map((project) => ({
			id: hostedProjectKey(project),
			label: multipleHosts && project.hostName ? `${project.name} · ${project.hostName}` : project.name,
		})),
	];
}
