/**
 * The approval mode a new worker starts in: how much it may do before asking.
 * The daemon's values, with the desktop's labels for them so both apps name a
 * mode the same way.
 */
export type ApprovalMode = "default" | "auto" | "accept-edits" | "bypass-permissions";

export type ApprovalOption = { id: ApprovalMode; label: string; shortLabel: string };

/** Auto is what a new session gets when neither the spawn nor the project picks. */
export const FALLBACK_APPROVAL_MODE: ApprovalMode = "auto";

export function isApprovalMode(value: unknown): value is ApprovalMode {
	return value === "default" || value === "auto" || value === "accept-edits" || value === "bypass-permissions";
}

/**
 * The modes an agent can start in, in the desktop's order. Codex has no
 * permission mode of its own for "default" to defer to, and opencode cannot
 * accept edits without also running commands.
 */
export function approvalOptions(harness: string): ApprovalOption[] {
	const modes: ApprovalMode[] = [
		...(harness === "codex" ? [] : ["default" as const]),
		"auto",
		...(harness === "opencode" ? [] : ["accept-edits" as const]),
		"bypass-permissions",
	];
	return modes.map((id) => ({ id, label: approvalLabel(id, harness), shortLabel: approvalShortLabel(id) }));
}

export function approvalLabel(mode: ApprovalMode, harness: string): string {
	switch (mode) {
		case "default":
			return harness === "claude-code" ? "Use Claude permissions" : "Use agent permissions";
		case "accept-edits":
			return "Accept edits";
		case "bypass-permissions":
			return "Bypass permissions";
		default:
			return "Auto";
	}
}

/** The mode's name where a whole label has no room: a control beside the agent. */
export function approvalShortLabel(mode: ApprovalMode): string {
	switch (mode) {
		case "default":
			return "Ask";
		case "accept-edits":
			return "Edits";
		case "bypass-permissions":
			return "Bypass";
		default:
			return "Auto";
	}
}

/**
 * The mode the picker shows: the one chosen here, else the project's own, else
 * Auto, so the picker always names what the worker will actually start in. A
 * mode the agent cannot start in falls back to Auto rather than naming it.
 */
export function resolveApprovalMode({ harness, chosen, projectMode }: { harness: string; chosen?: ApprovalMode; projectMode?: unknown }): ApprovalMode {
	const allowed = new Set(approvalOptions(harness).map((option) => option.id));
	if (chosen && allowed.has(chosen)) return chosen;
	if (isApprovalMode(projectMode) && allowed.has(projectMode)) return projectMode;
	return FALLBACK_APPROVAL_MODE;
}
