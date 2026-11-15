import type { CloudCpCoderTemplateParameter, CloudCpOrgCoderConfig } from "./cloud-cp/types";

const AO_MANAGED_PARAMETERS: ReadonlySet<string> = new Set(["size", "startup_script"]);

export function editableTemplateParameters(parameters: readonly CloudCpCoderTemplateParameter[]): CloudCpCoderTemplateParameter[] {
	return parameters
		.filter((parameter) => !AO_MANAGED_PARAMETERS.has(parameter.name))
		.map((parameter, index) => ({ parameter, index }))
		.sort((a, b) => (a.parameter.order ?? 0) - (b.parameter.order ?? 0) || a.index - b.index)
		.map(({ parameter }) => parameter);
}

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

function canonicalValue(parameter: CloudCpCoderTemplateParameter, value: string): string {
	if (parameter.type === "list(string)") return encodeListValue(decodeListValue(value));
	if (parameter.type === "bool") return value.trim().toLowerCase() === "true" ? "true" : "false";
	if (parameter.type === "number") return value.trim() === "" ? "" : String(Number(value));
	return value;
}

export function isDefaultParameterValue(parameter: CloudCpCoderTemplateParameter, value: string): boolean {
	return canonicalValue(parameter, value) === canonicalValue(parameter, parameter.defaultValue ?? "");
}

export function effectiveParameterValue(parameter: CloudCpCoderTemplateParameter, overrides: Readonly<Record<string, string>>): string {
	return overrides[parameter.name] ?? parameter.defaultValue ?? "";
}

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

export function coderReusesTemplateCheckout(
	config: Pick<CloudCpOrgCoderConfig, "workerUser" | "reuseExistingCheckout" | "workingDirectory"> | null,
	projectWorkingDirectory = "",
): boolean {
	if (config?.workerUser !== "workspace") return false;
	return (config.reuseExistingCheckout ?? true)
		|| (config.workingDirectory ?? "").trim() !== ""
		|| projectWorkingDirectory.trim() !== "";
}

function bareRepoName(value: string): string {
	const trimmed = value.trim().replace(/[/\\]+$/, "").replace(/\.git$/i, "");
	return (trimmed.split(/[/:]/).pop() ?? "").trim().toLowerCase();
}

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
			const matches = names.has(bareRepoName(value)) || names.has(bareRepoName(option?.name ?? ""));
			return matches ? [option?.name?.trim() || value] : [];
		});
		if (labels.length > 0) result.set(parameter.name, labels);
	}
	return result;
}
