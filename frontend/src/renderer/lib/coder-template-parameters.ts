import type { CloudCpCoderTemplateParameter, CloudCpOrgCoderConfig } from "./cloud-cp/types";

// Template parameters AO sets itself from the project's own controls (the
// machine size select and the startup script), after any project override. They
// stay out of "Template options" so one value never has two editors.
const AO_MANAGED_PARAMETERS: ReadonlySet<string> = new Set(["size", "startup_script"]);

/** The parameters "Template options" lists, in the template's display order. */
export function editableTemplateParameters(parameters: readonly CloudCpCoderTemplateParameter[]): CloudCpCoderTemplateParameter[] {
	return parameters
		.filter((parameter) => !AO_MANAGED_PARAMETERS.has(parameter.name))
		.map((parameter, index) => ({ parameter, index }))
		.sort((a, b) => (a.parameter.order ?? 0) - (b.parameter.order ?? 0) || a.index - b.index)
		.map(({ parameter }) => parameter);
}

/** Decodes a list(string) value (a JSON array string); anything else is an empty list. */
export function decodeListValue(value: string | undefined): string[] {
	if (!value) return [];
	try {
		const parsed: unknown = JSON.parse(value);
		return Array.isArray(parsed) ? parsed.map((item) => String(item)) : [];
	} catch {
		return [];
	}
}

export function encodeListValue(items: readonly string[]): string {
	return JSON.stringify(items);
}

/** A value in comparable form: lists re-encoded, bools lower-cased, everything else trimmed. */
function canonicalValue(parameter: CloudCpCoderTemplateParameter, value: string): string {
	if (parameter.type === "list(string)") return encodeListValue(decodeListValue(value));
	if (parameter.type === "bool") return value.trim().toLowerCase() === "true" ? "true" : "false";
	if (parameter.type === "number") return value.trim() === "" ? "" : String(Number(value));
	return value;
}

export function isDefaultParameterValue(parameter: CloudCpCoderTemplateParameter, value: string): boolean {
	return canonicalValue(parameter, value) === canonicalValue(parameter, parameter.defaultValue ?? "");
}

/** The value a control shows: the override when there is one, else the template default. */
export function effectiveParameterValue(parameter: CloudCpCoderTemplateParameter, overrides: Readonly<Record<string, string>>): string {
	return overrides[parameter.name] ?? parameter.defaultValue ?? "";
}

/**
 * Applies one edit to an override map: a value equal to the template default
 * drops the key, so the map only ever carries values the user changed.
 */
export function withParameterValue(
	overrides: Readonly<Record<string, string>>,
	parameter: CloudCpCoderTemplateParameter,
	value: string,
): Record<string, string> {
	const next = { ...overrides };
	if (isDefaultParameterValue(parameter, value)) delete next[parameter.name];
	else next[parameter.name] = value;
	return next;
}

/**
 * The `coder.parameters` map to save so the project holds `draft`, or
 * undefined when it already does. The project settings endpoint replaces the
 * map as a whole, so the whole draft is sent: a key reset to its default is
 * simply absent, and a value whose parameter no longer applies (dropped by the
 * form after an edit) is removed with it.
 */
export function parameterOverridesUpdate(
	parameters: readonly CloudCpCoderTemplateParameter[],
	saved: Readonly<Record<string, string>>,
	draft: Readonly<Record<string, string>>,
): Record<string, string> | undefined {
	const current = changedParameterOverrides(parameters, saved);
	const sameKeys = Object.keys(current).length === Object.keys(draft).length;
	if (sameKeys && Object.entries(draft).every(([name, value]) => current[name] === value)) return undefined;
	return { ...draft };
}

/** Saved overrides minus those equal to the current template default. */
export function changedParameterOverrides(
	parameters: readonly CloudCpCoderTemplateParameter[],
	saved: Readonly<Record<string, string>> | undefined,
): Record<string, string> {
	const result: Record<string, string> = {};
	if (!saved) return result;
	const byName = new Map(parameters.map((parameter) => [parameter.name, parameter]));
	for (const [name, value] of Object.entries(saved)) {
		const parameter = byName.get(name);
		if (typeof value !== "string") continue;
		if (parameter && isDefaultParameterValue(parameter, value)) continue;
		result[name] = value;
	}
	return result;
}

/**
 * Whether sessions work in the template's own checkout instead of the one AO
 * clones: only with the workspace user, and then when the connection reuses a
 * matching checkout under ~/work (on unless turned off) or names a working
 * folder (the connection's, or the project's own). Mirrors the control plane's
 * OrgCoderConfig.ReusesExistingCheckout and the working directory override.
 * No connection (null) means the default ao-worker.
 */
export function coderReusesTemplateCheckout(
	config: Pick<CloudCpOrgCoderConfig, "workerUser" | "reuseExistingCheckout" | "workingDirectory"> | null,
	projectWorkingDirectory = "",
): boolean {
	if (config?.workerUser !== "workspace") return false;
	return (config.reuseExistingCheckout ?? true)
		|| (config.workingDirectory ?? "").trim() !== ""
		|| projectWorkingDirectory.trim() !== "";
}

/** A repository-ish string reduced to its bare name: "11x-ai/alice.git" gives "alice". */
function bareRepoName(value: string): string {
	const trimmed = value.trim().replace(/[/\\]+$/, "").replace(/\.git$/i, "");
	return (trimmed.split(/[/:]/).pop() ?? "").trim().toLowerCase();
}

/**
 * The selected options of each list(string) parameter that name one of the
 * repositories AO checks out itself, keyed by parameter name. Kept
 * conservative: an option counts only when its label or value, reduced to a
 * bare repository name, equals a repository's name (case-insensitive).
 */
export function duplicateCloneOptions(
	parameters: readonly CloudCpCoderTemplateParameter[],
	overrides: Readonly<Record<string, string>>,
	repositoryUrls: readonly string[],
): Map<string, string[]> {
	const result = new Map<string, string[]>();
	const names = new Set(repositoryUrls.map(bareRepoName).filter((name) => name !== ""));
	if (names.size === 0) return result;
	for (const parameter of parameters) {
		if (parameter.type !== "list(string)") continue;
		const options = parameter.options ?? [];
		const labels = decodeListValue(effectiveParameterValue(parameter, overrides)).flatMap((value) => {
			const option = options.find((item) => item.value === value);
			// names never holds "", so an empty label or value matches nothing.
			const matches = names.has(bareRepoName(value)) || names.has(bareRepoName(option?.name ?? ""));
			return matches ? [option?.name?.trim() || value] : [];
		});
		if (labels.length > 0) result.set(parameter.name, labels);
	}
	return result;
}
