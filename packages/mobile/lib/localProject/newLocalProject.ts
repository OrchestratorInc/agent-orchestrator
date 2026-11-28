import { ApiError } from "../api";
import { userFacingError } from "../connectionError";
import type { DirectoryEntry } from "./api";

// The rules of a new project on one of the user's machines, kept apart from
// its React so they can be tested: when Create is allowed, the request it
// sends, the names the form suggests and what each failure says.

/** An existing folder on the machine, a repository cloned into a new one, or a new, empty one. */
export type LocalProjectSource = "folder" | "clone" | "new";

/** Where clones and new projects land unless another folder is chosen; the desktop's default too. */
export const DEFAULT_CLONE_PARENT = "~/ao/projects";

/** The daemon's limit on a project's display name. */
export const LOCAL_PROJECT_NAME_MAX = 100;

export type LocalProjectDraft = {
	source: LocalProjectSource;
	/** The chosen folder, for `folder`. */
	folder: string | null;
	remoteUrl: string;
	destinationParent: string;
	name: string;
};

export type LocalProjectRequest =
	| { source: "folder"; input: { path: string; name?: string } }
	| { source: "clone"; input: { remoteUrl: string; destinationParent: string; name?: string } }
	| { source: "new"; input: { path: string; name: string } };

export function canCreateLocalProject({ source, folder, remoteUrl, destinationParent, name }: LocalProjectDraft): boolean {
	if (name.trim().length > LOCAL_PROJECT_NAME_MAX) return false;
	if (source === "folder") return Boolean(folder);
	if (source === "new") return folderNameFor(name).length > 0 && destinationParent.trim().length > 0;
	const url = remoteUrl.trim();
	return url.length > 0 && !/\s/.test(url) && destinationParent.trim().length > 0;
}

/** The create call for a draft. A name goes only when one was typed: the daemon names the project after its folder otherwise. */
export function localProjectRequest({ source, folder, remoteUrl, destinationParent, name }: LocalProjectDraft): LocalProjectRequest {
	const displayName = name.trim();
	const named = displayName ? { name: displayName } : {};
	if (source === "folder") return { source, input: { path: folder ?? "", ...named } };
	if (source === "new") return { source, input: { path: joinPath(destinationParent.trim(), folderNameFor(name)), name: displayName } };
	return { source, input: { remoteUrl: remoteUrl.trim(), destinationParent: destinationParent.trim(), ...named } };
}

/**
 * The folder a new project's name becomes: its words joined by hyphens,
 * without path separators, characters folders can't hold, or a leading dot
 * that would hide it. "My App" becomes "My-App".
 */
export function folderNameFor(name: string): string {
	return name
		.trim()
		.replace(/[\\/:*?"<>|\u0000-\u001f]+/g, "-")
		.replace(/\s+/g, "-")
		.replace(/-{2,}/g, "-")
		// No leading dot, which would hide it, and no trailing dot, which Windows drops from the folder on disk.
		.replace(/^[.-]+|[.-]+$/g, "")
		.slice(0, LOCAL_PROJECT_NAME_MAX);
}

/** A child path under `parent`, with the separator the parent already uses. */
export function joinPath(parent: string, child: string): string {
	const separator = parent.includes("\\") && !parent.includes("/") ? "\\" : "/";
	return `${parent.replace(/[\\/]+$/, "")}${separator}${child}`;
}

/** Where a new project will be made, as the form shows it under the name. */
export function newProjectPathPreview(name: string, destinationParent: string): string | undefined {
	const folder = folderNameFor(name);
	const parent = destinationParent.trim();
	if (!folder || !parent) return undefined;
	return joinPath(parent, folder);
}

/** A path's last segment, for either separator; the path itself at a root. */
export function baseName(path: string): string {
	const trimmed = path.replace(/[\\/]+$/, "");
	const segment = trimmed.split(/[\\/]/).pop();
	return segment || path;
}

/**
 * The folder git would clone a URL into: its last path segment without
 * `.git`, for HTTPS, SSH (`git@host:owner/repo.git`) and file URLs alike.
 */
export function repositoryNameFromUrl(url: string): string {
	const trimmed = url.trim().replace(/[\\/]+$/, "").replace(/\.git$/i, "");
	const segment = trimmed.split(/[\\/:]/).pop() ?? "";
	return segment;
}

/** Where a clone will land, as the form shows it under the URL. */
export function clonePathPreview(url: string, destinationParent: string): string | undefined {
	const name = repositoryNameFromUrl(url);
	const parent = destinationParent.trim().replace(/[\\/]+$/, "");
	if (!name || !parent) return undefined;
	return `${parent}/${name}`;
}

/** Shortens a path under the machine user's home to `~/…`, as the desktop shows paths. */
export function displayPath(path: string, home: string | undefined): string {
	if (!home) return path;
	const root = home.replace(/[\\/]+$/, "");
	if (path === root) return "~";
	if (path.startsWith(`${root}/`) || path.startsWith(`${root}\\`)) return `~${path.slice(root.length)}`;
	return path;
}

/** A folder's title in a row: Home for the machine user's home, its own name otherwise. */
export function placeTitle(path: string, home: string | undefined): string {
	return displayPath(path, home) === "~" ? "Home" : baseName(path);
}

/**
 * The folders a filter leaves, repositories first, as typing narrows a long
 * listing. Case-insensitive, matching anywhere in the name.
 */
export function filterDirectories(entries: readonly DirectoryEntry[], filter: string): DirectoryEntry[] {
	const needle = filter.trim().toLowerCase();
	const matched = needle ? entries.filter((entry) => entry.name.toLowerCase().includes(needle)) : [...entries];
	return matched.sort((a, b) => Number(b.gitRepo) - Number(a.gitRepo) || a.name.localeCompare(b.name, undefined, { sensitivity: "base" }));
}

export const UNSUPPORTED_MACHINE = "Update Orchestrator on this machine to add projects from your phone.";
export const UNSUPPORTED_NEW_PROJECT = "Update Orchestrator on this machine to create new projects from your phone.";

/**
 * A machine from before new projects could be created rejects the request's
 * `create` field as unknown.
 */
export function createUnsupported(error: unknown): boolean {
	return unsupportedMachine(error) || (error instanceof ApiError && error.code === "INVALID_JSON");
}

/**
 * The machine runs a version without the route: a 404 that carries no daemon
 * code. A missing folder is a 404 too, but the daemon names it.
 */
export function unsupportedMachine(error: unknown): boolean {
	return error instanceof ApiError && (error.status === 404 || error.status === 405) && !error.code;
}

/** What a failed browse, add, initialize or clone says. */
export function localProjectErrorMessage(error: unknown): string {
	if (unsupportedMachine(error)) return UNSUPPORTED_MACHINE;
	if (error instanceof ApiError) {
		switch (error.code) {
			case "FS_NOT_FOUND": return "That folder no longer exists on the machine.";
			case "FS_FORBIDDEN": return "Orchestrator isn't allowed to read that folder.";
			case "PATH_ALREADY_REGISTERED": return "This folder is already a project.";
			case "CLONE_DESTINATION_EXISTS": return "A folder with this repository's name already exists there. Choose another destination.";
			case "PATH_EXISTS": return "A folder with this name already exists there. Choose another name or location.";
			case "GIT_NOT_FOUND": return "Install Git on this machine to clone repositories.";
		}
	}
	return userFacingError(error);
}
