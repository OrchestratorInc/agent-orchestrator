import type { CloudClient, Project, RedactedProviderConnection } from "@aoagents/cloud-client";

const HARNESS_PRIORITY = ["codex", "claude-code", "cursor"] as const;

function readyHarnesses(connections: readonly RedactedProviderConnection[]): Set<string> {
	return new Set(connections
		.filter((connection) => connection.label === "default" && connection.validationState === "valid")
		.map((connection) => connection.provider));
}

function projectHarness(project: Project | undefined): string | undefined {
	const orchestrator = project?.config.orchestrator;
	if (!orchestrator || typeof orchestrator !== "object") return undefined;
	const agent = (orchestrator as { agent?: unknown }).agent;
	return typeof agent === "string" ? agent.trim() : undefined;
}

/** Desktop Cloud parity: create one orchestrator session using the project's chosen agent when ready. */
export async function spawnCloudOrchestrator(
	client: CloudClient,
	orgId: string,
	projectId: string,
	idempotencyKey: string,
): Promise<string> {
	const [orgConnections, userConnections] = await Promise.all([
		client.listProviderConnections(orgId),
		client.listUserProviderConnections(),
	]);
	const ready = readyHarnesses([...orgConnections, ...userConnections]);
	// Project config is a preference, not a blocker if this fetch fails.
	const projects = await client.listProjects(orgId, { limit: 100 }).catch(() => null);
	const chosen = projectHarness(projects?.items.find((project) => project.id === projectId));
	const harness = chosen && ready.has(chosen) ? chosen : HARNESS_PRIORITY.find((candidate) => ready.has(candidate));
	if (!harness) throw new Error("Connect a Cloud coding agent before spawning an orchestrator.");
	const { session } = await client.createSession(orgId, {
		projectId,
		kind: "orchestrator",
		harness,
		displayName: "Orchestrator",
		prompt: "",
		mode: "trusted",
		deniedCommands: [],
	}, { idempotencyKey });
	return session.id;
}
