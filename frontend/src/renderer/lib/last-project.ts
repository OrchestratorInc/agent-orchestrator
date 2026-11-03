import { STANDALONE_PROJECT_KIND, type WorkspaceSummary } from "../types/workspace";

export const LAST_PROJECT_KEY = "ao.newTask.lastProject";

/** The last project the user was in, while it still exists. */
export function lastProjectId(workspaces: WorkspaceSummary[]): string | undefined {
	let id: string | null = null;
	try {
		id = localStorage.getItem(LAST_PROJECT_KEY);
	} catch {
		return undefined;
	}
	return workspaces.some((workspace) => workspace.id === id && workspace.kind !== STANDALONE_PROJECT_KIND) ? id ?? undefined : undefined;
}
