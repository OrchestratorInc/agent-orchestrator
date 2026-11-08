import { LOCAL_HOST, type HostId } from "./hosts";
import type { WorkspaceSummary } from "../types/workspace";

/** A project for one repository on one computer. */
export type RepoHost = { hostId: HostId; projectId: string; projectName: string };

/** Every computer with a project for repoKey: this one first, then remote hosts in list order. */
export function hostsWithRepo(local: WorkspaceSummary[], remote: WorkspaceSummary[], repoKey: string | undefined): RepoHost[] {
	if (!repoKey) return [];
	const out: RepoHost[] = [];
	const add = (workspace: WorkspaceSummary, hostId: HostId) => {
		if (workspace.repoKey === repoKey && !out.some((host) => host.hostId === hostId)) {
			out.push({ hostId, projectId: workspace.id, projectName: workspace.name });
		}
	};
	for (const workspace of local) add(workspace, LOCAL_HOST);
	for (const workspace of remote) if (workspace.hostId) add(workspace, workspace.hostId);
	return out;
}
