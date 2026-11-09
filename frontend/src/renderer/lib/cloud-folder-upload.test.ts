import { describe, expect, it, vi } from "vitest";
import { aoBridge } from "./bridge";
import { appI18n } from "../i18n";
import {
	createFolderRepository,
	describeLocalFolder,
	FolderPushError,
	folderPushErrorText,
	gitHubFullNameFromRemote,
	grantedRepositoryForRemote,
	pushFolderToCreatedRepository,
} from "./cloud-folder-upload";

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
		expect(createGitHubRepository).toHaveBeenCalledWith("org-1", { name: "notes", owner: "acme", description: "Uploaded from Orchestrator" });

		const push = vi.spyOn(aoBridge.cloud, "pushFolderToRepository").mockResolvedValue({ status: "pushed", branch: "trunk", commit: "c", source: "head", leftOut: [] });
		await pushFolderToCreatedRepository("/work/notes", created);
		expect(push).toHaveBeenCalledWith({ folder: "/work/notes", pushPath: "/p.git", branch: "trunk" });

		push.mockResolvedValue({ status: "error", code: "push_failed", message: "Could not upload notes: denied" });
		await expect(pushFolderToCreatedRepository("/work/notes", created)).rejects.toThrow("Could not upload notes: denied");
		push.mockRestore();
	});

	it("tells what uploading a folder sends: the checked-out branch's commits, a detached commit, or its files", async () => {
		const scan = vi.spyOn(aoBridge.app, "scanImportFolder");
		const branch = vi.spyOn(aoBridge.app, "getRepositoryBranch");
		const repo = (overrides: Record<string, unknown>) => ({ path: "/w/a", repos: [{ name: "a", path: "/w/a", relativePath: ".", branch: "auto", remote: "git@github.com:acme/a.git", hasRemote: true, isRepo: true, hasCommit: true, ...overrides }] });

		scan.mockResolvedValue(repo({})); branch.mockResolvedValue("feature-x");
		expect(await describeLocalFolder("/w/a")).toEqual({ remote: "git@github.com:acme/a.git", committed: { branch: "feature-x" } });
		branch.mockResolvedValue(undefined);
		expect(await describeLocalFolder("/w/a")).toEqual({ remote: "git@github.com:acme/a.git", committed: { branch: undefined } });
		scan.mockResolvedValue(repo({ hasCommit: false })); branch.mockResolvedValue("main");
		expect((await describeLocalFolder("/w/a")).committed).toBeUndefined();
		// A linked worktree's .git is a file, which the scan reads as a plain folder.
		scan.mockResolvedValue(repo({ isRepo: false, hasCommit: false, needsGitInit: true, remote: "" })); branch.mockResolvedValue("wt");
		expect(await describeLocalFolder("/w/a")).toEqual({ remote: "", committed: { branch: "wt" } });
		// A plain folder, and a scan or branch read that fails.
		branch.mockResolvedValue(undefined);
		expect(await describeLocalFolder("/w/a")).toEqual({ remote: "" });
		scan.mockRejectedValue(new Error("unreadable")); branch.mockRejectedValue(new Error("no git"));
		expect(await describeLocalFolder("/w/a")).toEqual({ remote: "" });
		scan.mockRestore();
		branch.mockRestore();
	});

	it("translates an upload refusal by its code, keeping the main process's message where it adds to it", () => {
		const t = appI18n.t.bind(appI18n);
		expect(folderPushErrorText(t, new FolderPushError("signed_out", "Your Orchestrator Cloud session expired. Sign in again, then try again."), "/w/notes"))
			.toEqual({ message: "Your Orchestrator Cloud session expired. Sign in again, then try again." });
		expect(folderPushErrorText(t, new FolderPushError("push_failed", "Could not upload notes: remote hung up"), "/w/notes/"))
			.toEqual({ message: "Could not upload notes.", detail: "Could not upload notes: remote hung up" });
		// A code this build does not know reads as a failed upload.
		expect(folderPushErrorText(t, new FolderPushError("brand_new" as never, "Something new"), "/w/notes"))
			.toEqual({ message: "Could not upload notes.", detail: "Something new" });
	});
});
