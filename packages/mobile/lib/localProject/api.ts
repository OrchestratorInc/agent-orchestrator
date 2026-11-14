import { apiRequest } from "../api";
import type { ServerConfig } from "../config";

// The daemon routes the desktop already uses to add a project on another
// machine (CreateProjectFlow, RemoteDirectoryPicker), called from the phone.

/** One subdirectory in a listing: dotted names are never listed. */
export type DirectoryEntry = { name: string; path: string; gitRepo: boolean };

/** A directory on the machine and its visible subdirectories. */
export type DirectoryListing = {
	path: string;
	/** Equals `path` at the filesystem root. */
	parent: string;
	entries: DirectoryEntry[];
	/** The daemon stopped at its cap; more subdirectories exist. */
	truncated: boolean;
};

/** A clone holds the request open while git downloads; the daemon allows 30 minutes. */
const CLONE_TIMEOUT_MS = 31 * 60_000;
/** git init plus a first commit, on a folder that may hold many files. */
const INITIALIZE_TIMEOUT_MS = 2 * 60_000;

/** The subdirectories of an absolute path on the machine; with none, its user's home. */
export async function listDirectories(cfg: ServerConfig, path?: string, init?: Pick<RequestInit, "signal">): Promise<DirectoryListing> {
	const query = path ? `?path=${encodeURIComponent(path)}` : "";
	const res = await apiRequest(cfg, `/api/v1/fs/dirs${query}`, init);
	const data = await res.json() as Partial<DirectoryListing> | null;
	const listed = typeof data?.path === "string" ? data.path : path ?? "";
	return {
		path: listed,
		parent: typeof data?.parent === "string" ? data.parent : listed,
		entries: Array.isArray(data?.entries)
			? data.entries.filter((entry): entry is DirectoryEntry => typeof entry?.name === "string" && typeof entry?.path === "string").map((entry) => ({ name: entry.name, path: entry.path, gitRepo: Boolean(entry.gitRepo) }))
			: [],
		truncated: Boolean(data?.truncated),
	};
}

async function projectId(res: Response): Promise<string> {
	const data = await res.json() as { project?: { id?: unknown } } | null;
	const id = data?.project?.id;
	if (typeof id !== "string" || !id) throw new Error("The machine didn't return the new project.");
	return id;
}

/** Registers a folder on the machine that already holds a Git repository with a commit. */
export async function addFolderProject(cfg: ServerConfig, input: { path: string; name?: string; clonePreparationId?: string }): Promise<string> {
	const res = await apiRequest(cfg, "/api/v1/projects", { method: "POST", body: JSON.stringify(input) });
	return projectId(res);
}

/**
 * Makes a plain folder, or a repository without commits, ready to add: git
 * init and a first commit. With `create`, makes the folder first, which must
 * not exist yet: a new, empty project.
 */
export async function initializeFolder(cfg: ServerConfig, path: string, options: { create?: boolean; createId?: string } = {}): Promise<void> {
	const body = options.create ? { path, create: true, ...(options.createId ? { createId: options.createId } : {}) } : { path };
	await apiRequest(cfg, "/api/v1/projects/initialize", { method: "POST", body: JSON.stringify(body) }, INITIALIZE_TIMEOUT_MS);
}


// ---- The desktop's import steps -------------------------------------------
// The desktop validates a folder, runs the Git setup it needs one step at a
// time, and only then registers it. The phone takes the same steps, so the
// two accept the same folders and leave them in the same state.

export type ImportAction = "git_init" | "git_commit" | "create_remote_repository" | "set_remote";

export type ImportValidation = {
	isValid: boolean;
	blockingErrors: string[];
	nextStep: "error" | "choose_import_kind" | "prepare_git" | "continue";
	root: { repoPath: string; isRepo: boolean; hasCommit: boolean; hasOrigin: boolean; requiredActions: ImportAction[]; blockingErrors: string[] };
	childRepos?: { repoPath: string }[];
	warning?: string;
};

export type ImportEvent = { action: ImportAction; repoPath: string; state: "pending" | "running" | "success" | "error"; message?: string; error?: string };

export type GitHubRepositoryInput = { owner: string; name: string; private: boolean };

export async function validateImport(cfg: ServerConfig, path: string): Promise<ImportValidation> {
	const res = await apiRequest(cfg, "/api/v1/imports/validate", { method: "POST", body: JSON.stringify({ importKind: "project", path }) });
	return await res.json() as ImportValidation;
}

/** One approved Git setup step, as the desktop runs them; the validation after it says what is left. */
export async function prepareImportGit(cfg: ServerConfig, input: {
	path: string;
	approvedActions: ImportAction[];
	remoteUrl?: string;
	githubRepository?: GitHubRepositoryInput;
}): Promise<{ events: ImportEvent[]; validation: ImportValidation }> {
	const body = {
		importKind: "project",
		path: input.path,
		approvedActions: input.approvedActions,
		...(input.remoteUrl ? { remoteUrl: input.remoteUrl } : {}),
		...(input.githubRepository ? { githubRepository: input.githubRepository } : {}),
		stepwise: true,
	};
	// Creating a GitHub repository and pushing can outlast an ordinary request.
	const res = await apiRequest(cfg, "/api/v1/imports/prepare-git", { method: "POST", body: JSON.stringify(body) }, INITIALIZE_TIMEOUT_MS);
	const data = await res.json() as { events?: ImportEvent[]; validation: ImportValidation };
	return { events: Array.isArray(data.events) ? data.events : [], validation: data.validation };
}

/** Clones without registering, as the desktop does, so the checkout is validated first. */
export async function prepareClone(cfg: ServerConfig, input: { remoteUrl: string; destinationParent: string }): Promise<{ path: string; preparationId: string }> {
	const res = await apiRequest(cfg, "/api/v1/projects/clone/prepare", { method: "POST", body: JSON.stringify(input) }, CLONE_TIMEOUT_MS);
	const data = await res.json() as { path?: string; preparationId?: string };
	if (!data.path || !data.preparationId) throw new Error("The machine didn't return the clone.");
	return { path: data.path, preparationId: data.preparationId };
}

/** Removes a prepared clone that will not become a project. */
export async function cleanupClone(cfg: ServerConfig, input: { path: string; preparationId: string }): Promise<void> {
	await apiRequest(cfg, "/api/v1/projects/clone/cleanup", { method: "POST", body: JSON.stringify(input) });
}

export type GitHubOwner = { login: string; avatarUrl: string };

/** The accounts gh on the machine can create a repository under, the user first; none when gh is signed out. */
export async function listGitHubOwners(cfg: ServerConfig): Promise<GitHubOwner[]> {
	const res = await apiRequest(cfg, "/api/v1/imports/github-owners", undefined, 15_000);
	const data = await res.json() as { owners?: GitHubOwner[] };
	return Array.isArray(data.owners) ? data.owners.filter((owner) => typeof owner?.login === "string" && owner.login) : [];
}

export async function checkGitHubAvailability(cfg: ServerConfig, owner: string, name: string, init?: Pick<RequestInit, "signal">): Promise<{ available: boolean; message?: string }> {
	const query = `?owner=${encodeURIComponent(owner)}&name=${encodeURIComponent(name)}`;
	const res = await apiRequest(cfg, `/api/v1/imports/github-availability${query}`, init, 15_000);
	const data = await res.json() as { available?: boolean; message?: string };
	return { available: data.available === true, ...(data.message ? { message: data.message } : {}) };
}
