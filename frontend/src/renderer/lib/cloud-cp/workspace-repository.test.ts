import { describe, expect, it, vi } from "vitest";
import type { CloudCpClient } from "./client";
import { joinWorkspaceRepositoryPath, splitWorkspaceRepositoryPath, withWorkspaceRepository, workspaceRepositoryOf } from "./workspace-repository";

describe("workspace repository scoping", () => {
	it("adds the repository to every workspace call and keeps the main client as is", async () => {
		const getWorkspaceReview = vi.fn().mockResolvedValue({});
		const getWorkspaceReviewTree = vi.fn().mockResolvedValue({});
		const client = { getWorkspaceReview, getWorkspaceReviewTree } as unknown as CloudCpClient;
		expect(withWorkspaceRepository(client, "")).toBe(client);
		const scoped = withWorkspaceRepository(client, "11x");
		expect(withWorkspaceRepository(client, "11x")).toBe(scoped);
		expect(workspaceRepositoryOf(scoped)).toBe("11x");
		expect(workspaceRepositoryOf(client)).toBeUndefined();
		await scoped.getWorkspaceReview("org", "s");
		await scoped.getWorkspaceReviewTree("org", "s", "src");
		expect(getWorkspaceReview).toHaveBeenCalledWith("org", "s", { repository: "11x" });
		expect(getWorkspaceReviewTree).toHaveBeenCalledWith("org", "s", "src", { repository: "11x" });
	});

	it("scopes the branch, history and selection calls of a review view", async () => {
		const getWorkspaceBranches = vi.fn().mockResolvedValue({});
		const getWorkspaceHistory = vi.fn().mockResolvedValue({});
		const getWorkspaceCommitSelection = vi.fn().mockResolvedValue({});
		const scoped = withWorkspaceRepository({ getWorkspaceBranches, getWorkspaceHistory, getWorkspaceCommitSelection } as unknown as CloudCpClient, "docs");
		await scoped.getWorkspaceBranches("org", "s");
		await scoped.getWorkspaceHistory("org", "s", { branch: "main" });
		await scoped.getWorkspaceCommitSelection("org", "s", { branch: "main", commitShas: [] });
		expect(getWorkspaceBranches).toHaveBeenCalledWith("org", "s", { repository: "docs" });
		expect(getWorkspaceHistory).toHaveBeenCalledWith("org", "s", { branch: "main" }, { repository: "docs" });
		expect(getWorkspaceCommitSelection).toHaveBeenCalledWith("org", "s", { branch: "main", commitShas: [] }, { repository: "docs" });
	});

	it("addresses sibling files as ../<repository>/<path>", () => {
		expect(joinWorkspaceRepositoryPath("11x", "src/a.ts")).toBe("../11x/src/a.ts");
		expect(joinWorkspaceRepositoryPath(undefined, "src/a.ts")).toBe("src/a.ts");
		expect(splitWorkspaceRepositoryPath("../11x/src/a.ts")).toEqual({ repository: "11x", path: "src/a.ts" });
		expect(splitWorkspaceRepositoryPath("src/a.ts")).toEqual({ path: "src/a.ts" });
	});
});
