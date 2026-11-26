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
	leftOutNotice,
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

	it("tells what uploading a folder sends as the main process decides it: the checked-out branch's commits, a detached commit, or its files", async () => {
		const scan = vi.spyOn(aoBridge.app, "scanImportFolder");
		const branch = vi.spyOn(aoBridge.app, "getRepositoryBranch");
		const preview = vi.spyOn(aoBridge.cloud, "previewFolderUpload");
		const repo = (overrides: Record<string, unknown>) => ({ path: "/w/a", repos: [{ name: "a", path: "/w/a", relativePath: ".", branch: "auto", remote: "git@github.com:acme/a.git", hasRemote: true, isRepo: true, hasCommit: true, ...overrides }] });

		scan.mockResolvedValue(repo({}));
		preview.mockResolvedValue({ status: "ok", source: "head", branch: "feature-x", leftOut: [] });
		expect(await describeLocalFolder("/w/a")).toEqual({ remote: "git@github.com:acme/a.git", committed: { branch: "feature-x" } });
		preview.mockResolvedValue({ status: "ok", source: "head", leftOut: [] });
		expect(await describeLocalFolder("/w/a")).toEqual({ remote: "git@github.com:acme/a.git", committed: { branch: undefined } });
		// The scan sees a repository with a commit, but the main process imports
		// the folder's files (say git could not read the repository's HEAD as
		// the folder's own): the main process decides.
		preview.mockResolvedValue({ status: "ok", source: "import", leftOut: [".env"] });
		expect(await describeLocalFolder("/w/a")).toEqual({ remote: "git@github.com:acme/a.git", leftOut: [".env"] });
		// A linked worktree's .git is a file, which the scan reads as a plain folder.
		scan.mockResolvedValue(repo({ isRepo: false, hasCommit: false, needsGitInit: true, remote: "" }));
		preview.mockResolvedValue({ status: "ok", source: "head", branch: "wt", leftOut: [] });
		expect(await describeLocalFolder("/w/a")).toEqual({ remote: "", committed: { branch: "wt" } });
		// A scan that fails still says what goes; a preview that fails says nothing.
		scan.mockRejectedValue(new Error("unreadable"));
		preview.mockResolvedValue({ status: "ok", source: "import", leftOut: [] });
		expect(await describeLocalFolder("/w/a")).toEqual({ remote: "" });
		preview.mockRejectedValue(new Error("no bridge"));
		expect(await describeLocalFolder("/w/a")).toEqual({ remote: "" });
		// The branch comes from the main process, never read separately.
		expect(branch).not.toHaveBeenCalled();
		scan.mockRestore();
		branch.mockRestore();
		preview.mockRestore();
	});

	it("refuses a repository git cannot read instead of calling it a folder of files", async () => {
		const scan = vi.spyOn(aoBridge.app, "scanImportFolder").mockResolvedValue({ path: "/w/a", repos: [] });
		const message = "git could not read a as a repository, so Orchestrator cannot tell whether to upload its commits or its files: fatal: detected dubious ownership in repository at '/w/a'";
		const preview = vi.spyOn(aoBridge.cloud, "previewFolderUpload").mockResolvedValue({ status: "error", code: "import_failed", message });

		const info = await describeLocalFolder("/w/a");

		expect(info.committed).toBeUndefined();
		expect(info.refusal).toBeInstanceOf(FolderPushError);
		expect(folderPushErrorText(appI18n.t.bind(appI18n), info.refusal!, "/w/a")).toEqual({ message: "Orchestrator could not prepare a for upload.", detail: message });
		scan.mockRestore();
		preview.mockRestore();
	});

	it("reads the folder's origin and the main process's preview at once", async () => {
		let finishScan: (value: { path: string; repos: never[] }) => void = () => undefined;
		const scan = vi.spyOn(aoBridge.app, "scanImportFolder").mockImplementation(() => new Promise((resolve) => { finishScan = resolve; }));
		const preview = vi.spyOn(aoBridge.cloud, "previewFolderUpload").mockResolvedValue({ status: "ok", source: "import", leftOut: [] });

		const described = describeLocalFolder("/w/a");
		await Promise.resolve();

		expect(preview).toHaveBeenCalledWith({ folder: "/w/a" });
		finishScan({ path: "/w/a", repos: [] });
		expect(await described).toEqual({ remote: "" });
		scan.mockRestore();
		preview.mockRestore();
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

	it("names the secret-looking files a refused folder's history holds, once", () => {
		const t = appI18n.t.bind(appI18n);
		expect(folderPushErrorText(t, new FolderPushError("secret_files", "notes's history holds ... (.env)", [".env"]), "/w/notes"))
			.toEqual({ message: "The history of notes holds files that look like secrets (.env), so Orchestrator does not upload it. Remove them from its history, or push it to GitHub yourself and add its repository from GitHub." });
		expect(folderPushErrorText(t, new FolderPushError("secret_files", "...", [".env", "a.pem", "id_rsa", "x.key", "y.p12"]), "/w/notes").message)
			.toContain("(.env, a.pem, id_rsa and 2 more)");
	});

	it("names up to four left-out files before counting the rest", () => {
		const t = appI18n.t.bind(appI18n);
		expect(leftOutNotice(t, [".env", ".ssh/config", "config/.env.production", "id_rsa"]))
			.toBe("Leaves out files that look like secrets: .env, .ssh/config, config/.env.production, id_rsa");
		expect(leftOutNotice(t, [".env", "a.pem", "id_rsa", "x.key", "y.p12"]))
			.toBe("Leaves out files that look like secrets: .env, a.pem, id_rsa and 2 more");
	});
});
