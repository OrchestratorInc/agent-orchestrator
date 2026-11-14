import { LOCAL_HOST, type HostId } from "./hosts";
import type { WorkspaceSummary } from "../types/workspace";

/** A project for one repository on one computer. */
export type RepoHost = { hostId: HostId; projectId: string; projectName: string };

/** Every project for repoKey: this computer's first, then remote hosts' in list order. */
export function projectsWithRepo(local: WorkspaceSummary[], remote: WorkspaceSummary[], repoKey: string | undefined): RepoHost[] {
	if (!repoKey) return [];
	const out: RepoHost[] = [];
	const add = (workspace: WorkspaceSummary, hostId: HostId) => {
		if (workspace.repoKey === repoKey) out.push({ hostId, projectId: workspace.id, projectName: workspace.name });
	};
	for (const workspace of local) add(workspace, LOCAL_HOST);
	for (const workspace of remote) if (workspace.hostId) add(workspace, workspace.hostId);
	return out;
}

/** Every computer with a project for repoKey, each with its first such project. */
export function hostsWithRepo(local: WorkspaceSummary[], remote: WorkspaceSummary[], repoKey: string | undefined): RepoHost[] {
	return projectsWithRepo(local, remote, repoKey).filter((project, index, all) => all.findIndex((other) => other.hostId === project.hostId) === index);
}
