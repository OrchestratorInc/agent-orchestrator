/** The workspace name prefix new Coder workspaces get when a project sets none. */
export const DEFAULT_CODER_WORKSPACE_NAME_PREFIX = "ao";

const PREFIX_PATTERN = /^[a-z][a-z0-9-]{0,19}$/;

/**
 * Mirrors the control plane's validation of a project's Coder workspace name
 * prefix: empty means the default, otherwise 1-20 lowercase letters, digits or
 * hyphens starting with a letter, with no trailing hyphen and no `--` run.
 */
export function isValidCoderWorkspaceNamePrefix(value: string): boolean {
	if (value === "") return true;
	return PREFIX_PATTERN.test(value) && !value.endsWith("-") && !value.includes("--");
}
