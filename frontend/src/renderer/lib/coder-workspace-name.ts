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

/**
 * The prefix a bring-your-own Coder workspace gets when its project sets none:
 * a slug of the project name, mirroring the control plane's
 * domain.CoderWorkspaceNamePrefixFor (lower-cased, each run of other
 * characters one hyphen, starting with a letter, at most 20 characters), or
 * the default prefix when the name has no letters.
 */
export function coderWorkspaceNamePrefixFor(projectName: string): string {
	let slug = "";
	let hyphen = false;
	for (const character of projectName.toLowerCase()) {
		const letter = character >= "a" && character <= "z";
		const digit = character >= "0" && character <= "9";
		if (letter || (digit && slug.length > 0)) {
			if (hyphen && slug.length > 0) slug += "-";
			hyphen = false;
			slug += character;
		} else {
			hyphen = true;
		}
		if (slug.length >= 20) break;
	}
	const prefix = slug.slice(0, 20).replace(/-+$/, "");
	return prefix !== "" && isValidCoderWorkspaceNamePrefix(prefix) ? prefix : DEFAULT_CODER_WORKSPACE_NAME_PREFIX;
}
