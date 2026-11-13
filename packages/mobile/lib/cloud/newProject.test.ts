import { describe, expect, it } from "vitest";
import {
	canCreateProject,
	changedInstallation,
	hasActiveInstallation,
	matchRepositories,
	newProjectRequest,
	PROJECT_NAME_MAX,
	reachableRepositories,
	repositoryDetail,
	repositorySearchHint,
} from "./newProject";
import type { CloudGitHubInstallation, CloudGitHubRepository } from "./types";

function repo(fullName: string, extra: Partial<CloudGitHubRepository> = {}): CloudGitHubRepository {
	return {
		githubRepositoryId: fullName,
		name: fullName.split("/")[1] ?? fullName,
		fullName,
		htmlUrl: `https://github.com/${fullName}`,
		defaultBranch: "main",
		visibility: "private",
		isPrivate: true,
		isArchived: false,
		access: "admin",
		grantedAt: "2026-01-01T00:00:00Z",
		...extra,
	};
}

function installation(id: string, status: string, updatedAt: string): CloudGitHubInstallation {
	return {
		id,
		githubInstallationId: `gh-${id}`,
		accountLogin: "acme",
		accountType: "Organization",
		status,
		repositorySelection: "selected",
		syncStatus: "synced",
		createdAt: "2026-01-01T00:00:00Z",
		updatedAt,
	};
}

describe("new cloud project rules", () => {
	it("offers only repositories that are neither archived nor revoked", () => {
		const items = [repo("acme/api"), repo("acme/old", { isArchived: true }), repo("acme/gone", { revokedAt: "2026-02-01T00:00:00Z" })];
		expect(reachableRepositories(items).map((item) => item.fullName)).toEqual(["acme/api"]);
	});

	it("shows no repositories until something is typed, then matches the full name ignoring case", () => {
		const repos = [repo("acme/API"), repo("acme/web"), repo("tools/apiary")];
		expect(matchRepositories(repos, "")).toEqual([]);
		expect(matchRepositories(repos, "   ")).toEqual([]);
		expect(matchRepositories(null, "api")).toEqual([]);
		expect(matchRepositories(repos, " Api ").map((item) => item.fullName)).toEqual(["acme/API", "tools/apiary"]);
		expect(matchRepositories(repos, "acme/").map((item) => item.fullName)).toEqual(["acme/API", "acme/web"]);
	});

	it("caps the matches so a large organization stays a short list", () => {
		const repos = Array.from({ length: 30 }, (_, index) => repo(`acme/service-${index}`));
		expect(matchRepositories(repos, "service")).toHaveLength(20);
		expect(matchRepositories(repos, "service", 5)).toHaveLength(5);
	});

	it("says how many repositories the search covers", () => {
		expect(repositorySearchHint(0)).toMatch(/can't see any repositories/);
		expect(repositorySearchHint(1)).toBe("Type to find one of 1 repository the app can reach.");
		expect(repositorySearchHint(12)).toBe("Type to find one of 12 repositories the app can reach.");
	});

	it("describes a repository by its visibility and default branch", () => {
		expect(repositoryDetail({ isPrivate: true, defaultBranch: "main" })).toBe("Private · main");
		expect(repositoryDetail({ isPrivate: false, defaultBranch: "" })).toBe("Public");
	});

	it("knows when an installation is usable and which one an install just changed", () => {
		expect(hasActiveInstallation(null)).toBe(false);
		expect(hasActiveInstallation([installation("a", "suspended", "t1")])).toBe(false);
		expect(hasActiveInstallation([installation("a", "suspended", "t1"), installation("b", "active", "t1")])).toBe(true);

		const before = new Map([["a", "t1"]]);
		expect(changedInstallation([installation("a", "active", "t1")], before)).toBeUndefined();
		expect(changedInstallation([installation("a", "active", "t2")], before)?.id).toBe("a");
		expect(changedInstallation([installation("a", "active", "t1"), installation("b", "active", "t1")], before)?.id).toBe("b");
		expect(changedInstallation([installation("b", "suspended", "t1")], before)).toBeUndefined();
	});

	it("needs a repository for a GitHub project and a name for a standalone one", () => {
		expect(canCreateProject({ source: "github", repoId: null, name: "Anything" })).toBe(false);
		expect(canCreateProject({ source: "github", repoId: "r1", name: "" })).toBe(true);
		expect(canCreateProject({ source: "standalone", repoId: "r1", name: "   " })).toBe(false);
		expect(canCreateProject({ source: "standalone", repoId: null, name: " Notes " })).toBe(true);
		expect(canCreateProject({ source: "standalone", repoId: null, name: "x".repeat(PROJECT_NAME_MAX + 1) })).toBe(false);
	});

	it("sends a repository project's name only when one was typed", () => {
		expect(newProjectRequest({ source: "github", repoId: "r1", name: "  ", workerAgent: "claude-code", orchestratorAgent: "codex" })).toEqual({
			source: "github",
			input: { githubRepositoryId: "r1", config: { worker: { agent: "claude-code" }, orchestrator: { agent: "codex" } } },
		});
		expect(newProjectRequest({ source: "github", repoId: "r1", name: " API ", workerAgent: undefined, orchestratorAgent: undefined })).toEqual({
			source: "github",
			input: { githubRepositoryId: "r1", displayName: "API", config: {} },
		});
	});

	it("sends a standalone project's trimmed name and only the agents picked", () => {
		expect(newProjectRequest({ source: "standalone", repoId: "r1", name: " Notes ", workerAgent: "cursor" })).toEqual({
			source: "standalone",
			input: { displayName: "Notes", config: { worker: { agent: "cursor" } } },
		});
	});
});
