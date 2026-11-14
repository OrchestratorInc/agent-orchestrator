import { CLOUD_PROJECT_KIND, type WorkspaceSummary } from "../types/workspace";
import { LOCAL_HOST, refKey } from "../lib/hosts";

const removedProjectIds = new Set<string>();
let removalVersion = 0;
const removalListeners = new Set<() => void>();

function notifyRemovalChange() {
	removalVersion += 1;
	for (const listener of removalListeners) listener();
}

export function subscribeOptimisticProjectRemovals(listener: () => void): () => void {
	removalListeners.add(listener);
	return () => removalListeners.delete(listener);
}

export function optimisticProjectRemovalsVersion(): number {
	return removalVersion;
}

function projectKey(projectId: string, hostId?: string, cloud = false): string {
	return cloud && !hostId ? `@cloud:${encodeURIComponent(projectId)}` : refKey({ host: hostId ?? LOCAL_HOST, id: projectId });
}

function workspaceKey(workspace: WorkspaceSummary): string {
	return projectKey(workspace.id, workspace.hostId, workspace.kind === CLOUD_PROJECT_KIND);
}

export function trackOptimisticProjectRemoval(projectId: string, hostId?: string, cloud = false): void {
	const key = projectKey(projectId, hostId, cloud);
	if (removedProjectIds.has(key)) return;
	removedProjectIds.add(key);
	notifyRemovalChange();
}

export function clearOptimisticProjectRemoval(projectId: string, hostId?: string, cloud = false): void {
	if (removedProjectIds.delete(projectKey(projectId, hostId, cloud))) notifyRemovalChange();
}

export function isOptimisticallyRemovedProject(projectId: string, hostId?: string, cloud = false): boolean {
	return removedProjectIds.has(projectKey(projectId, hostId, cloud));
}

export function applyOptimisticProjectRemovals<T extends WorkspaceSummary[] | undefined>(workspaces: T): T {
	if (!workspaces || removedProjectIds.size === 0) return workspaces;
	const kept = workspaces.filter((workspace) => !removedProjectIds.has(workspaceKey(workspace)));
	return (kept.length === workspaces.length ? workspaces : kept) as T;
}
