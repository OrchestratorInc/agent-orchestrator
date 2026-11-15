import type { TFunction } from "i18next";
import type { PushFolderPreview, PushFolderResult } from "../../preload";
import { aoBridge } from "./bridge";
import type { CloudCpClient, CloudCpGitHubAppRepository } from "./cloud-cp";
import { canonicalGitHubUrl } from "./cloud-project-repos";

export type CreatedFolderRepository = {
	repository: CloudCpGitHubAppRepository;
	pushPath: string;
};

export function gitHubFullNameFromRemote(remote: string): string | undefined {
	const value = remote.trim();
	const match = /^(?:https:\/\/(?:[^@/]+@)?(?:www\.)?github\.com\/|ssh:\/\/git@github\.com(?::\d+)?\/|git@github\.com:)([A-Za-z0-9-]+)\/([A-Za-z0-9._-]+?)(?:\.git)?\/?$/i.exec(value);
	return match ? `${match[1]}/${match[2]}` : undefined;
}

export function grantedRepositoryForRemote(remote: string, granted: readonly CloudCpGitHubAppRepository[]): CloudCpGitHubAppRepository | undefined {
	const fullName = gitHubFullNameFromRemote(remote);
	if (!fullName) return undefined;
	return granted.find((repo) => repo.fullName.toLowerCase() === fullName.toLowerCase());
}

export function repositoryOwner(fullName: string): string {
	return fullName.split("/")[0] ?? "";
}

export function folderLabel(path: string): string {
	return path.replace(/[\\/]+$/, "").split(/[\\/]/).pop() || path;
}

export type LocalFolderInfo = {
	remote: string;
	committed?: { branch?: string };
	leftOut?: string[];
	refusal?: FolderPushError;
};

export async function describeLocalFolder(path: string): Promise<LocalFolderInfo> {
	const settle = async <T,>(read: () => Promise<T>): Promise<T | undefined> => {
		try {
			return await read();
		} catch {
			return undefined;
		}
	};
	const [scanned, preview] = await Promise.all([
		settle(async () => (await aoBridge.app.scanImportFolder({ path, mode: "project" })).repos[0]),
		settle<PushFolderPreview>(() => aoBridge.cloud.previewFolderUpload({ folder: path })),
	]);
	const remote = scanned?.remote ?? "";
	if (preview?.status === "error") {
		return preview.code === "invalid_request" ? { remote } : { remote, refusal: new FolderPushError(preview.code, preview.message, preview.files) };
	}
	return {
		remote,
		...(preview?.source === "head" ? { committed: { branch: preview.branch } } : {}),
		...(preview?.leftOut.length ? { leftOut: preview.leftOut } : {}),
	};
}

const NAMED_FILES = 4;

function shownFileNames(files: readonly string[]): string {
	return (files.length > NAMED_FILES ? files.slice(0, 3) : files).join(", ");
}

function secretFilesText(t: TFunction, folder: string, files: readonly string[]): string {
	const shown = shownFileNames(files);
	return files.length > NAMED_FILES
		? t("cloudWorkspace.pushError.secretFilesMore", { defaultValue: "The history of {{folder}} holds files that look like secrets ({{files}} and {{count}} more), so Orchestrator does not upload it. Remove them from its history, or push it to GitHub yourself and add its repository from GitHub.", folder, files: shown, count: files.length - 3 })
		: t("cloudWorkspace.pushError.secretFiles", { defaultValue: "The history of {{folder}} holds files that look like secrets ({{files}}), so Orchestrator does not upload it. Remove them from its history, or push it to GitHub yourself and add its repository from GitHub.", folder, files: shown });
}

export function leftOutNotice(t: TFunction, files: readonly string[]): string {
	const shown = shownFileNames(files);
	return files.length > NAMED_FILES
		? t("cloudWorkspace.leftOutSecretsMore", { defaultValue: "Leaves out files that look like secrets: {{files}} and {{count}} more", files: shown, count: files.length - 3 })
		: t("cloudWorkspace.leftOutSecrets", { defaultValue: "Leaves out files that look like secrets: {{files}}", files: shown });
}

export function committedUploadHint(t: TFunction, committed: { branch?: string }): string {
	return committed.branch
		? t("cloudWorkspace.uploadsCommits", { defaultValue: "Uploads the commits of {{branch}}, not uncommitted changes", branch: committed.branch })
		: t("cloudWorkspace.uploadsCheckedOutCommit", { defaultValue: "Uploads the checked-out commit and its history, not uncommitted changes" });
}

export async function createFolderRepository(
	client: Pick<CloudCpClient, "createGitHubRepository">,
	orgId: string,
	input: { name: string; owner?: string },
): Promise<CreatedFolderRepository> {
	const { repository, pushPath } = await client.createGitHubRepository(orgId, {
		name: input.name,
		...(input.owner ? { owner: input.owner } : {}),
		description: "Uploaded from Orchestrator",
	});
	return { repository, pushPath };
}

export class FolderPushError extends Error {
	constructor(
		readonly code: Extract<PushFolderResult, { status: "error" }>["code"],
		message: string,
		readonly files?: readonly string[],
	) {
		super(message);
		this.name = "FolderPushError";
	}
}

export async function pushFolderToCreatedRepository(folder: string, created: CreatedFolderRepository): Promise<void> {
	const result = await aoBridge.cloud.pushFolderToRepository({
		folder,
		pushPath: created.pushPath,
		branch: created.repository.defaultBranch || "main",
	});
	if (result.status !== "pushed") throw new FolderPushError(result.code, result.message, result.files);
}

export function createdRepositoryUrl(created: CreatedFolderRepository): string {
	return canonicalGitHubUrl(created.repository.fullName);
}

export function emptyRepositoryNotice(t: TFunction, created: CreatedFolderRepository, folder: string): string {
	return t("cloudWorkspace.emptyRepositoryLeft", {
		defaultValue: "{{name}} is on GitHub but still empty. Try again to upload into it. If you don't, it stays on GitHub empty: delete it there, or add {{folder}} again under the same name to upload into it later.",
		name: created.repository.fullName,
		folder: folderLabel(folder),
	});
}

const PUSH_ERROR_TEXT: Record<string, (t: TFunction, folder: string) => string> = {
	invalid_request: (t, folder) => t("cloudWorkspace.pushError.invalidRequest", { defaultValue: "Orchestrator could not upload {{folder}}.", folder }),
	signed_out: (t) => t("cloudWorkspace.pushError.signedOut", { defaultValue: "Your Orchestrator Cloud session expired. Sign in again, then try again." }),
	folder_unavailable: (t, folder) => t("cloudWorkspace.pushError.folderUnavailable", { defaultValue: "{{folder}} cannot be uploaded.", folder }),
	folder_too_large: (t, folder) => t("cloudWorkspace.pushError.folderTooLarge", { defaultValue: "{{folder}} is too large to upload. Add its large files to its .gitignore, or make it a git repository and add it from GitHub.", folder }),
	folder_has_repositories: (t, folder) => t("cloudWorkspace.pushError.folderHasRepositories", { defaultValue: "{{folder}} contains other git repositories or submodules, whose files would not be uploaded. Add each of them to the workspace on its own.", folder }),
	git_unavailable: (t) => t("cloudWorkspace.pushError.gitUnavailable", { defaultValue: "Uploading a folder needs git 2.31 or newer on this computer." }),
	import_failed: (t, folder) => t("cloudWorkspace.pushError.importFailed", { defaultValue: "Orchestrator could not prepare {{folder}} for upload.", folder }),
	push_failed: (t, folder) => t("cloudWorkspace.pushError.pushFailed", { defaultValue: "Could not upload {{folder}}.", folder }),
	push_forbidden: (t, folder) => t("cloudWorkspace.pushError.pushForbidden", { defaultValue: "Orchestrator Cloud did not let you upload {{folder}} to its repository. Check that the Orchestrator GitHub App still has access to it, then try again.", folder }),
	branch_exists: (t, folder) => t("cloudWorkspace.pushError.branchExists", { defaultValue: "The repository for {{folder}} already has commits, and Orchestrator only uploads a folder into an empty repository. If an earlier upload finished, remove {{folder}} and add its repository from GitHub instead.", folder }),
	shallow_repository: (t, folder) => t("cloudWorkspace.pushError.shallowRepository", { defaultValue: "{{folder}} is a shallow clone, which GitHub does not accept as a new repository. Fetch its full history (git fetch --unshallow), then try again.", folder }),
	lfs_unsupported: (t, folder) => t("cloudWorkspace.pushError.lfsUnsupported", { defaultValue: "{{folder}} uses Git LFS, which folder uploads do not support. Push it to GitHub yourself, then add it as a repository.", folder }),
	file_too_large: (t, folder) => t("cloudWorkspace.pushError.fileTooLarge", { defaultValue: "{{folder}} has a file larger than GitHub's 100 MB limit. Add it to its .gitignore, then try again.", folder }),
	workflows_refused: (t, folder) => t("cloudWorkspace.pushError.workflowsRefused", { defaultValue: "GitHub refused {{folder}} because it contains .github/workflows files and the Orchestrator GitHub App lacks the workflows permission. Remove them or grant the permission, then try again.", folder }),
};

const SELF_EXPLAINED_CODES = new Set(["signed_out", "push_forbidden", "branch_exists", "shallow_repository", "lfs_unsupported"]);

export function folderPushErrorText(t: TFunction, error: FolderPushError, folder: string): { message: string; detail?: string } {
	if (error.code === "secret_files" && error.files?.length) return { message: secretFilesText(t, folderLabel(folder), error.files) };
	const text = PUSH_ERROR_TEXT[error.code] ?? PUSH_ERROR_TEXT.push_failed;
	const message = text(t, folderLabel(folder));
	const detail = error.message.trim();
	return SELF_EXPLAINED_CODES.has(error.code) || detail === "" || detail === message ? { message } : { message, detail };
}
