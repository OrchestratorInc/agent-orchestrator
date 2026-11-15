export const DEFAULT_CODER_WORKSPACE_NAME_PREFIX = "ao";

const PREFIX_PATTERN = /^[a-z][a-z0-9-]{0,19}$/;

export function isValidCoderWorkspaceNamePrefix(value: string): boolean {
	if (value === "") return true;
	return PREFIX_PATTERN.test(value) && !value.endsWith("-") && !value.includes("--");
}

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
