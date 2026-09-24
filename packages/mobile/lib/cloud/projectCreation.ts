import type { CreateProjectInput } from "@aoagents/cloud-client";

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
	workerAgent: string;
	orchestratorAgent: string;
}): CreateProjectInput {
	return {
		displayName: input.displayName.trim(),
		repositoryUrl: input.repositoryUrl.trim(),
		defaultBranch: input.defaultBranch.trim(),
		config: {
			worker: { agent: input.workerAgent },
			orchestrator: { agent: input.orchestratorAgent },
		},
	};
}
