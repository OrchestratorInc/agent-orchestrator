import type { DashboardSession, ProjectInfo } from "./api";
import { resourceKey, type Scoped } from "./environment/scopedBoard";
import type { EnvironmentKind } from "./environment/types";

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

export function spawnProjectParam(projectId: string): { projectId: string } | undefined {
	return projectId === ALL_WORKER_PROJECTS ? undefined : { projectId };
}

export function filterWorkersByProject<T extends { projectId: string }>(
	workers: readonly T[],
	projectId: string,
): T[] {
	if (projectId === ALL_WORKER_PROJECTS) return [...workers];
	return workers.filter((worker) => worker.projectId === projectId);
}

export function workerSearchPresentation(
	requestedOpen: boolean,
	query: string,
): "collapsed" | "expanded" {
	return requestedOpen || query.trim().length > 0 ? "expanded" : "collapsed";
}

export function workerProjectLabel(
	projects: readonly { id: string; name: string }[],
	projectId: string,
): string {
	if (projectId === ALL_WORKER_PROJECTS) return "All projects";
	return projects.find((project) => project.id === projectId)?.name ?? "All projects";
}

export function workerProjectOptions(
	projects: readonly { id: string; name: string }[],
): { id: string; label: string }[] {
	return [
		{ id: ALL_WORKER_PROJECTS, label: "All projects" },
		...projects.map((project) => ({ id: project.id, label: project.name })),
	];
}
