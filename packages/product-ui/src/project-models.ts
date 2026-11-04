export type ProjectKind = "single_repo" | "workspace" | "scratch";

export type ProjectRepositorySummary = {
	name: string;
	relativePath: string;
	repo?: string;
};

export type ProjectSettingsValues = {
	displayName: string;
	defaultBranch: string;
	sessionPrefix: string;
	permissions: string;
	reviewerHarness: string;
	intakeEnabled: boolean;
	intakeRepo: string;
	intakeAssignee: string;
};

export const MAX_PROJECT_DISPLAY_NAME_LEN = 100;

export type ProjectSettingsValidationCode =
	| "name_required"
	| "name_too_long"
	| "intake_assignee_required";

export function validateProjectSettings(
	values: Pick<
		ProjectSettingsValues,
		"displayName" | "intakeEnabled" | "intakeAssignee"
	>,
	options: { validateIntake?: boolean; originalDisplayName?: string } = {},
): ProjectSettingsValidationCode | null {
	const trimmedName = values.displayName.trim();
	if (trimmedName === "") return "name_required";
	if (
		Array.from(trimmedName).length > MAX_PROJECT_DISPLAY_NAME_LEN &&
		trimmedName !== options.originalDisplayName?.trim()
	) {
		return "name_too_long";
	}
	if (options.validateIntake !== false && values.intakeEnabled && values.intakeAssignee.trim() === "") {
		return "intake_assignee_required";
	}
	return null;
}

export type ProjectSetupSelection = {
	intakeEnabled?: boolean;
	intakeAssignee?: string;
};

export function canSubmitProjectSetup(selection: ProjectSetupSelection): boolean {
	return !selection.intakeEnabled || (selection.intakeAssignee?.trim() ?? "") !== "";
}
