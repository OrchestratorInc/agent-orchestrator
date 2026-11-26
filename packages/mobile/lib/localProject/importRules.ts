import type { ImportAction, ImportEvent, ImportValidation } from "./api";

// The desktop's rules for importing a folder (CreateProjectFlow), kept the
// same here so a folder the phone accepts is one the desktop would, set up the
// same way: its order of Git setup steps, their names, what blocks a folder,
// and what a remote must look like.

export const IMPORT_ACTION_ORDER: readonly ImportAction[] = ["git_init", "git_commit", "create_remote_repository", "set_remote"];

const ACTION_LABELS: Record<ImportAction, string> = {
	git_init: "Git initialization",
	git_commit: "Initial commit",
	create_remote_repository: "Create remote repository",
	set_remote: "Remote setup",
};

export function actionLabel(action: ImportAction): string {
	return ACTION_LABELS[action] ?? "Git setup";
}

export function orderedActions(actions: readonly ImportAction[]): ImportAction[] {
	const rank = (action: ImportAction) => {
		const index = IMPORT_ACTION_ORDER.indexOf(action);
		return index < 0 ? IMPORT_ACTION_ORDER.length : index;
	};
	return [...actions].sort((a, b) => rank(a) - rank(b));
}

/** The folder has no origin yet: a GitHub repository is created for it, or a remote URL is given. */
export function needsRemote(actions: readonly ImportAction[]): boolean {
	return actions.includes("create_remote_repository") || actions.includes("set_remote");
}

/** The desktop's words for each reason a folder can't be imported. */
export function blockingErrorLabel(code: string): string {
	switch (code) {
		case "INVALID_PATH": return "Choose a folder Orchestrator can read.";
		case "PATH_NOT_DIRECTORY": return "Choose a folder, not a file.";
		case "BARE_REPOSITORY": return "Choose a normal working checkout instead of a bare Git repository.";
		case "UNSUPPORTED_GIT_METADATA": return "Repair the Git metadata or choose a different folder.";
		case "CHILD_REPO_SCAN_FAILED": return "Orchestrator could not inspect the repositories under this folder.";
		case "IMPORT_PATH_UNSAFE": return "Choose a specific project folder outside Orchestrator's own state directories.";
		case "DETACHED_HEAD": return "This repository is checked out in detached HEAD state. Check out a named branch before importing it.";
		case "WORKSPACE_CHILD_REPO_REQUIRED": return "This workspace needs at least one direct child Git repository.";
		default: return "Choose a different folder or repair the repository before continuing.";
	}
}

export const WORKSPACE_ONLY = "This folder contains child Git repositories. Add it as a workspace from Orchestrator on your computer.";

/** What a validation lets the form do next. */
export type ImportState =
	| { kind: "ready" }
	| { kind: "setup"; actions: ImportAction[] }
	| { kind: "blocked"; message: string };

export function importState(validation: ImportValidation): ImportState {
	if (!validation.isValid || validation.nextStep === "error") {
		const codes = validation.blockingErrors.length ? validation.blockingErrors : validation.root.blockingErrors;
		return { kind: "blocked", message: codes.length ? codes.map(blockingErrorLabel).join(" ") : "This folder cannot be imported yet." };
	}
	// The desktop asks project or workspace here; workspaces are made on the computer.
	if (validation.nextStep === "choose_import_kind") return { kind: "blocked", message: WORKSPACE_ONLY };
	if (validation.nextStep === "prepare_git") return { kind: "setup", actions: orderedActions(validation.root.requiredActions) };
	return { kind: "ready" };
}

/** The desktop's check for a remote URL: HTTPS, SSH (also scp-style), Git, or file. */
export function isValidRemoteUrl(value: string): boolean {
	if (!value || /\s/.test(value) || value.startsWith("-")) return false;
	if (/^[^/@:\s]+@[^/\s]+:.+\/.+$/.test(value)) return true;
	try {
		const parsed = new URL(value);
		return ["file:", "git:", "http:", "https:", "ssh:"].includes(parsed.protocol)
			&& parsed.pathname.split("/").filter(Boolean).length >= 1;
	} catch {
		return false;
	}
}

/** GitHub repository names have no spaces or .git suffix; the desktop rewrites them the same way. */
export function normalizeGitHubName(value: string): string {
	return value.trim().replace(/\s+/g, "-").replace(/\.git$/i, "");
}

/** A folder's name as a GitHub repository's, as the desktop suggests it. */
export function suggestedGitHubName(path: string): string {
	return normalizeGitHubName(path.split(/[\\/]/).filter(Boolean).pop() ?? "") || "repository";
}

/** Why a Git setup step failed, in the desktop's terms. */
export function preparationFailureMessage(event: ImportEvent): string {
	const detail = (event.error || event.message || "").trim();
	return `${actionLabel(event.action)} failed${detail ? `: ${detail}` : "."}`;
}

/** The step that runs next, if the daemon is still asking for one. */
export function nextAction(validation: ImportValidation): ImportAction | undefined {
	return validation.nextStep === "prepare_git" ? orderedActions(validation.root.requiredActions)[0] : undefined;
}
