import { describe, expect, it, vi } from "vitest";
import { aoBridge } from "./bridge";
import { createFolderRepository, gitHubFullNameFromRemote, grantedRepositoryForRemote, pushFolderToCreatedRepository } from "./cloud-folder-upload";

const repository = (fullName: string, defaultBranch = "main") => ({
	githubRepositoryId: "7", name: fullName.split("/")[1] ?? "", fullName, htmlUrl: `https://github.com/${fullName}`,
	defaultBranch, visibility: "private", isPrivate: true, isArchived: false, access: "write", grantedAt: "",
});

describe("cloud folder upload", () => {
	it("reads a github.com origin in every form git writes it", () => {
		for (const remote of ["https://github.com/acme/web.git", "https://github.com/acme/web", "git@github.com:acme/web.git", "ssh://git@github.com/acme/web.git", "https://token@github.com/acme/web.git"]) {
			expect(gitHubFullNameFromRemote(remote), remote).toBe("acme/web");
		}
		expect(gitHubFullNameFromRemote("https://gitlab.com/acme/web.git")).toBeUndefined();
		expect(gitHubFullNameFromRemote("")).toBeUndefined();
	});

	it("matches a folder's origin to a granted repository ignoring case", () => {
		expect(grantedRepositoryForRemote("git@github.com:Acme/Web.git", [repository("acme/web")])?.fullName).toBe("acme/web");
		expect(grantedRepositoryForRemote("git@github.com:acme/other.git", [repository("acme/web")])).toBeUndefined();
	});

	it("creates the repository in the chosen account and pushes to its default branch", async () => {
		const createGitHubRepository = vi.fn().mockResolvedValue({ repository: repository("acme/notes", "trunk"), created: true, pushPath: "/p.git" });
		const created = await createFolderRepository({ createGitHubRepository }, "org-1", { name: "notes", owner: "acme" });
		expect(createGitHubRepository).toHaveBeenCalledWith("org-1", { name: "notes", owner: "acme", description: "Uploaded from AO" });

		const push = vi.spyOn(aoBridge.cloud, "pushFolderToRepository").mockResolvedValue({ status: "pushed", branch: "trunk", commit: "c", source: "head" });
		await pushFolderToCreatedRepository("/work/notes", created);
		expect(push).toHaveBeenCalledWith({ folder: "/work/notes", pushPath: "/p.git", branch: "trunk" });

		push.mockResolvedValue({ status: "error", code: "push_failed", message: "Could not upload notes: denied" });
		await expect(pushFolderToCreatedRepository("/work/notes", created)).rejects.toThrow("Could not upload notes: denied");
		push.mockRestore();
	});
});
