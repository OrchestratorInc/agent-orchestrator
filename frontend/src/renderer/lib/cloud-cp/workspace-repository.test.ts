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

	it("addresses sibling files as ../<repository>/<path>", () => {
		expect(joinWorkspaceRepositoryPath("11x", "src/a.ts")).toBe("../11x/src/a.ts");
		expect(joinWorkspaceRepositoryPath(undefined, "src/a.ts")).toBe("src/a.ts");
		expect(splitWorkspaceRepositoryPath("../11x/src/a.ts")).toEqual({ repository: "11x", path: "src/a.ts" });
		expect(splitWorkspaceRepositoryPath("src/a.ts")).toEqual({ path: "src/a.ts" });
	});
});
