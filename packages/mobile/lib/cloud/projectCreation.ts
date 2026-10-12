import type { CloudClient, CreateProjectInput, CoderTemplateParameter, OrgCoderConfigResponse } from "@aoagents/cloud-client";

export function normalizedGitHubRepository(raw: string): string {
	return raw.trim().replace(/\/$/, "").replace(/\.git$/i, "").toLowerCase();
}
export async function findCloudProjectForRepository(client: CloudClient, orgId: string, repositoryUrl: string, signal?: AbortSignal): Promise<string | null> {
	const seen = new Set<string>();
	let cursor: string | undefined;
	for (let page = 0; page < 100; page += 1) {
		if (signal?.aborted) throw new Error("Project lookup cancelled.");
		const result = await client.listProjects(orgId, { limit: 100, cursor, signal });
		if (signal?.aborted) throw new Error("Project lookup cancelled.");
		const existing = result.items.find((project) => normalizedGitHubRepository(project.repositoryUrl) === normalizedGitHubRepository(repositoryUrl));
		if (existing) return existing.id;
		if (!result.page.hasMore) return null;
		cursor = result.page.nextCursor;
		if (!cursor || seen.has(cursor)) throw new Error("Cloud project pagination did not advance. Retry the lookup.");
		seen.add(cursor);
	}
	throw new Error("Cloud project lookup exceeded its page limit.");
}

export function coderSetupProblem(config: OrgCoderConfigResponse, templates: readonly { id: string }[], templateId: string): string | null {
	if (config.configured && !templateId) return "Choose a Coder template before creating this project.";
	if (templateId && !templates.some((template) => template.id === templateId)) return "This Coder template is no longer available. Choose another template.";
	return null;
}
export function coderParameterProblem(parameter: CoderTemplateParameter, value: string): string | null {
	const label = parameter.displayName || parameter.name;
	if (parameter.required && !value.trim()) return `${label} is required.`;
	if (!value.trim() && !parameter.required) return null;
	let values = [value];
	if (parameter.type === "number" && !Number.isFinite(Number(value))) return `Enter a number for ${label}.`;
	if (parameter.type === "bool" && !["true", "false"].includes(value)) return `Choose true or false for ${label}.`;
	if (parameter.type === "list(string)") {
		try {
			const parsed: unknown = JSON.parse(value);
			if (!Array.isArray(parsed) || !parsed.every((item) => typeof item === "string")) throw new Error();
			values = parsed;
		} catch { return `Enter a JSON list of strings for ${label}.`; }
	}
	if (!["string", "number", "bool", "list(string)"].includes(parameter.type)) return `Configure ${label} in desktop Settings.`;
	if (parameter.options.length && values.some((item) => !parameter.options.some((option) => option.value === item))) return `Choose a supported value for ${label}.`;
	if (/^(secret|token|accessToken|refreshToken|password|authorization|apiKey|privateKey)$/i.test(parameter.name)) return `Configure ${label} in desktop Settings; credentials are not collected here.`;
	return null;
}

export function initialProjectCreationStep(connections: Array<{ provider: string; validationState: string }>): "github-token" | "repository" {
	return connections.some((connection) => connection.provider === "github" && connection.validationState === "valid")
		? "repository" : "github-token";
}

export function validGitHubRepositoryURL(raw: string): boolean {
	// RN's URL polyfill is incomplete on-device; an anchored pattern also keeps
	// credentials, lookalike hosts, query strings, and non-repository paths out.
	return /^https:\/\/github\.com\/[a-z0-9-]+\/[a-z0-9._-]+\/?$/i.test(raw.trim());
}

export function cloudProjectInput(input: {
	displayName: string;
	repositoryUrl: string;
	defaultBranch: string;
	workerAgent?: string;
	orchestratorAgent?: string;
	config?: CreateProjectInput["config"];
}): CreateProjectInput {
	return {
		displayName: input.displayName.trim(),
		repositoryUrl: input.repositoryUrl.trim(),
		defaultBranch: input.defaultBranch.trim(),
		config: input.config ?? {
			worker: { agent: input.workerAgent },
			orchestrator: { agent: input.orchestratorAgent },
		},
	};
}
