import type { PlacementChoice } from "../localProject/LocalProjectForm.types";
import type { ProjectSource } from "./newProject";
import type { NewCloudProject } from "./useNewCloudProject";

export const NEW_PROJECT_TITLE = "New cloud project";

export const SOURCE_OPTIONS: readonly { key: ProjectSource; label: string }[] = [
	{ key: "github", label: "GitHub repository" },
	{ key: "standalone", label: "No repository" },
];

export const GITHUB_INSTALL_NOTE = "Install the Orchestrator GitHub App on the repositories agents should work in. You'll come back here when you're done.";
export const REPOSITORY_NAME_NOTE = "Leave it empty to use the repository's name.";
export const STANDALONE_NOTE = "Sessions start in an empty folder; agents can create a repository later.";
export const NO_AGENT_NOTE = "No coding agent is connected. You can create the project now and connect one before starting a session.";
export const WAITING_FOR_GITHUB = "Waiting for GitHub…";
export const REPOSITORIES_FAILED_NOTE = "Couldn't load the repositories the GitHub App can reach.";

/** The project's name field's hint, under it: what an empty name means for this kind of project. */
export function nameNote(source: ProjectSource): string {
	return source === "github" ? REPOSITORY_NAME_NOTE : STANDALONE_NOTE;
}

export function noMatchesNote(query: string): string {
	return `No repository matches “${query.trim()}”.`;
}

/**
 * The form: a SwiftUI Form in a sheet on iOS (./NewProjectForm), Material 3
 * fields and lists on a page on Android (./NewProjectForm.android). Only drawn once the
 * organization has loaded.
 */
export type NewProjectFormProps = {
	project: NewCloudProject;
	/** Opens Orchestrator Cloud's settings, where coding agents are connected. */
	onConnectAgent(): void;
	/** The Local and Cloud switch, when a paired machine could take the project too. */
	placement?: PlacementChoice;
};

/**
 * Cancel and Create, in the native bar: the sheet's on iOS, the page's top app
 * bar on Android, where back cancels.
 */
export type NewProjectBarProps = {
	canCreate: boolean;
	busy: boolean;
	onCancel(): void;
	onCreate(): void;
};
