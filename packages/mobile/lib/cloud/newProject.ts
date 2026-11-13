import type { CloudAgent } from "./agents";
import type { CloudGitHubInstallation, CloudGitHubRepository, CloudProjectConfig } from "./types";

// The rules of the New cloud project sheet, kept apart from its React so they
// can be tested: which repositories the search offers, when Create is allowed,
// and the request Create sends.

/** Where a new project's sessions start: a repository the GitHub App reaches, or an empty folder. */
export type ProjectSource = "github" | "standalone";

/** The control plane's limit on a project's display name. */
export const PROJECT_NAME_MAX = 120;

/** Matches the search shows at most; typing more narrows the rest. */
export const REPOSITORY_MATCH_LIMIT = 20;

/** Repositories a project can still be made from: not archived, and not taken back from the app. */
export function reachableRepositories(items: readonly CloudGitHubRepository[]): CloudGitHubRepository[] {
	return items.filter((item) => !item.revokedAt && !item.isArchived);
}

export function hasActiveInstallation(installations: readonly CloudGitHubInstallation[] | null | undefined): boolean {
	return Boolean(installations?.some((item) => item.status === "active"));
}

/**
 * The installation an install or reconfigure on GitHub just produced: one that
 * is active and is new, or changed since `before` (id to `updatedAt`) was taken.
 */
export function changedInstallation(found: readonly CloudGitHubInstallation[], before: ReadonlyMap<string, string>): CloudGitHubInstallation | undefined {
	return found.find((item) => item.status === "active" && before.get(item.id) !== item.updatedAt);
}

/**
 * Search, not a list: nothing until something is typed, then the repositories
 * whose full name contains it, ignoring case, up to `limit`.
 */
export function matchRepositories(repos: readonly CloudGitHubRepository[] | null | undefined, query: string, limit = REPOSITORY_MATCH_LIMIT): CloudGitHubRepository[] {
	const needle = query.trim().toLowerCase();
	if (!needle || !repos) return [];
	return repos.filter((item) => item.fullName.toLowerCase().includes(needle)).slice(0, limit);
}

/** What the search says before anything is typed. */
export function repositorySearchHint(count: number): string {
	if (count === 0) return "The app can't see any repositories yet. Add some on GitHub.";
	return `Type to find one of ${count} ${count === 1 ? "repository" : "repositories"} the app can reach.`;
}

/** A repository's second line: who can see it and the branch sessions start from. */
export function repositoryDetail(repo: Pick<CloudGitHubRepository, "isPrivate" | "defaultBranch">): string {
	const visibility = repo.isPrivate ? "Private" : "Public";
	return repo.defaultBranch ? `${visibility} · ${repo.defaultBranch}` : visibility;
}

export type NewProjectDraft = {
	source: ProjectSource;
	repoId: string | null;
	name: string;
	workerAgent?: CloudAgent;
	orchestratorAgent?: CloudAgent;
};

/** A repository project needs its repository; a standalone one needs a name, as the name is all it has. */
export function canCreateProject({ source, repoId, name }: Pick<NewProjectDraft, "source" | "repoId" | "name">): boolean {
	if (source === "github") return Boolean(repoId);
	const trimmed = name.trim();
	return trimmed.length > 0 && trimmed.length <= PROJECT_NAME_MAX;
}

export type NewProjectRequest =
	| { source: "github"; input: { githubRepositoryId: string; displayName?: string; config: CloudProjectConfig } }
	| { source: "standalone"; input: { displayName: string; config: CloudProjectConfig } };

/**
 * The create call for a draft. The roles carry only the agents picked, so a
 * project made with none connected takes the control plane's defaults. A
 * repository project sends a name only when one was typed; the control plane
 * names it after the repository otherwise.
 */
export function newProjectRequest({ source, repoId, name, workerAgent, orchestratorAgent }: NewProjectDraft): NewProjectRequest {
	const config: CloudProjectConfig = {
		...(workerAgent ? { worker: { agent: workerAgent } } : {}),
		...(orchestratorAgent ? { orchestrator: { agent: orchestratorAgent } } : {}),
	};
	const displayName = name.trim();
	if (source === "github") {
		return { source, input: { githubRepositoryId: repoId ?? "", ...(displayName ? { displayName } : {}), config } };
	}
	return { source, input: { displayName, config } };
}
