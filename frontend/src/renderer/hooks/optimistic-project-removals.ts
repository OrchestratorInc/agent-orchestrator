import type { WorkspaceSummary } from "../types/workspace";
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

function projectKey(projectId: string, hostId?: string): string {
	return refKey({ host: hostId ?? LOCAL_HOST, id: projectId });
}

export function trackOptimisticProjectRemoval(projectId: string, hostId?: string): void {
	const key = projectKey(projectId, hostId);
	if (removedProjectIds.has(key)) return;
	removedProjectIds.add(key);
	notifyRemovalChange();
}

export function clearOptimisticProjectRemoval(projectId: string, hostId?: string): void {
	if (removedProjectIds.delete(projectKey(projectId, hostId))) notifyRemovalChange();
}

export function isOptimisticallyRemovedProject(projectId: string, hostId?: string): boolean {
	return removedProjectIds.has(projectKey(projectId, hostId));
}

/** Drop pending removals. Returns the same array when nothing is dropped, so memoized readers keep their identity. */
export function applyOptimisticProjectRemovals<T extends WorkspaceSummary[] | undefined>(workspaces: T): T {
	if (!workspaces || removedProjectIds.size === 0) return workspaces;
	const kept = workspaces.filter((workspace) => !removedProjectIds.has(projectKey(workspace.id, workspace.hostId)));
	return (kept.length === workspaces.length ? workspaces : kept) as T;
}
