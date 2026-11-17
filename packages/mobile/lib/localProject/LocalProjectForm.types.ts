import type { LocalProjectSource } from "./newLocalProject";
import type { BrowseTarget, NewLocalProject } from "./useNewLocalProject";
import type { RemoteMode, RemoteSetup } from "./useRemoteSetup";

/** Where a new project lives: on one of the paired machines, or in Orchestrator Cloud. */
export type ProjectPlacement = "local" | "cloud";

export const PLACEMENT_OPTIONS: readonly { key: ProjectPlacement; label: string }[] = [
	{ key: "local", label: "Local" },
	{ key: "cloud", label: "Cloud" },
];

/**
 * The Local and Cloud switch at the top of either form, drawn only when both
 * can make a project.
 */
export type PlacementChoice = {
	value: ProjectPlacement;
	onChange(next: ProjectPlacement): void;
	/** A create is running: the switch holds still until it ends. */
	disabled?: boolean;
};

export const LOCAL_SOURCE_OPTIONS: readonly { key: LocalProjectSource; label: string }[] = [
	{ key: "folder", label: "Folder" },
	{ key: "clone", label: "Clone" },
	{ key: "new", label: "New" },
];

export const FOLDER_NAME_NOTE = "Leave it empty to use the folder's name.";
export const CLONE_NAME_NOTE = "Leave it empty to use the repository's name.";
export const NEW_NAME_PLACEHOLDER = "My project";
export const RECENT_TITLE = "Recent";
export const TRUNCATED_NOTE = "Only the first 500 folders are shown.";

/** Where a clone or a new project goes, as its section is titled. */
export function destinationTitle(source: LocalProjectSource): string {
	return source === "new" ? "Create in" : "Clone into";
}

export function browseTitle(target: BrowseTarget, source: LocalProjectSource): string {
	return target === "destination" ? destinationTitle(source) : "Folder";
}

/** The browsed folder becomes the choice: a project folder, or where a clone or new project goes. */
export function chooseHereLabel(target: BrowseTarget, source: LocalProjectSource): string {
	if (target === "folder") return "Use this folder";
	return source === "new" ? "Create it here" : "Clone here";
}

/** The name field's hint: what an empty name means, or where a new project's folder will be. */
export function nameNote(source: LocalProjectSource, newPath: string | undefined): string | undefined {
	if (source === "folder") return FOLDER_NAME_NOTE;
	if (source === "clone") return CLONE_NAME_NOTE;
	return newPath ? `Creates ${newPath}` : undefined;
}

/** Above the GitHub matches: how many repositories there are to search. */
export function githubSearchHint(count: number): string {
	return `Or find one of ${count} ${count === 1 ? "repository" : "repositories"} the Orchestrator GitHub App can reach.`;
}

export function noFolderMatchesNote(filter: string): string {
	return `No folder matches “${filter.trim()}”.`;
}

/** Under the URL: where the clone will land. */
export function cloneTargetNote(preview: string | undefined): string | undefined {
	return preview ? `Clones into ${preview}` : undefined;
}

export type LocalProjectFormProps = {
	project: NewLocalProject;
	placement?: PlacementChoice;
};

// ---- Git setup and the remote, as the desktop asks for them ----------------

export const SETUP_TITLE = "Git setup";
export const REMOTE_TITLE = "Remote";
export const CHECKING_FOLDER = "Checking this folder…";

export const REMOTE_MODE_OPTIONS: readonly { key: RemoteMode; label: string }[] = [
	{ key: "github", label: "New GitHub repo" },
	{ key: "url", label: "Remote URL" },
];

/** Under the remote: the name check, a bad URL, or what creating a repository needs. */
export function remoteFooter(remote: RemoteSetup, resuming = false): string | undefined {
	// The repository was made by the last try; only its push is left.
	if (resuming && remote.mode === "github") return `Retries pushing to github.com/${remote.owner}/${remote.name}`;
	if (remote.mode === "url") return remote.urlInvalid ? "Enter a valid HTTPS, SSH, Git, or file repository URL." : undefined;
	if (remote.owners === null) return "Checking GitHub on this machine…";
	if (!remote.owners.length) return "Sign in to GitHub CLI on this machine to create a repository, or use a remote URL.";
	switch (remote.availability.state) {
		case "checking": return "Checking the name…";
		case "available": return `Creates ${remote.isPrivate ? "a private" : "a public"} repository github.com/${remote.owner}/${remote.name}`;
		case "unavailable": return remote.availability.message;
		default: return undefined;
	}
}
