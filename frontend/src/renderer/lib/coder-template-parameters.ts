import type { CloudCpCoderTemplateParameter } from "./cloud-cp/types";

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
 * The `coder.parameters` patch that turns `saved` into `draft` on the project
 * settings endpoint, which merges the map key by key. A key reset to its
 * default is sent with the default value (a string the endpoint accepts), so the
 * stored override stops differing from the template either way. Undefined when
 * nothing changed.
 */
export function parameterOverridesPatch(
	parameters: readonly CloudCpCoderTemplateParameter[],
	saved: Readonly<Record<string, string>>,
	draft: Readonly<Record<string, string>>,
): Record<string, string> | undefined {
	const byName = new Map(parameters.map((parameter) => [parameter.name, parameter]));
	const patch: Record<string, string> = {};
	for (const [name, value] of Object.entries(draft)) {
		if (saved[name] !== value) patch[name] = value;
	}
	for (const name of Object.keys(saved)) {
		if (name in draft) continue;
		const parameter = byName.get(name);
		// A saved key the template no longer declares is left alone, as is one
		// already stored at the default.
		if (parameter && !isDefaultParameterValue(parameter, saved[name] ?? "")) patch[name] = parameter.defaultValue ?? "";
	}
	return Object.keys(patch).length > 0 ? patch : undefined;
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
