import { CLOUD_PROJECT_KIND, type WorkspaceSummary } from "../types/workspace";
import { LOCAL_HOST, refKey } from "../lib/hosts";

/**
 * Host-qualified projects the user removed whose removal the server has not
 * confirmed yet. Every workspace list drops them, so a poll or event refetch
 * that lands while the daemon is still tearing the project down cannot bring
 * the row back.
 */
const removedProjectIds = new Set<string>();
// Bumped on every change, so memoized lists that apply the removals (the merged
// cloud list, the sidebar's remote hosts) rebuild when one starts or ends.
let removalVersion = 0;
const removalListeners = new Set<() => void>();

function notifyRemovalChange() {
	removalVersion += 1;
	for (const listener of removalListeners) listener();
}

/** useSyncExternalStore source for the set of in-flight project removals. */
export function subscribeOptimisticProjectRemovals(listener: () => void): () => void {
	removalListeners.add(listener);
	return () => removalListeners.delete(listener);
}

export function optimisticProjectRemovalsVersion(): number {
	return removalVersion;
}

/**
 * Local and cloud projects both have no host, so a cloud project gets its own
 * scope: removing one never hides a local project with the same id. A host id
 * is URI-encoded and cannot contain "@".
 */
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

/** Drop pending removals. Returns the same array when nothing is dropped, so memoized readers keep their identity. */
export function applyOptimisticProjectRemovals<T extends WorkspaceSummary[] | undefined>(workspaces: T): T {
	if (!workspaces || removedProjectIds.size === 0) return workspaces;
	const kept = workspaces.filter((workspace) => !removedProjectIds.has(workspaceKey(workspace)));
	return (kept.length === workspaces.length ? workspaces : kept) as T;
}
