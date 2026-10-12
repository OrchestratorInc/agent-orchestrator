import type { CloudClient, Project } from "@aoagents/cloud-client";
import { readyHarnesses } from "./agentReadiness";
import { cloudRoleDefaults } from "../spawnModel";

const HARNESS_PRIORITY = ["codex", "claude-code", "cursor", "opencode"] as const;

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
	let cursor: string | undefined;
	const seen = new Set<string>();
	for (;;) {
		const page = await client.listSessions(orgId, { projectId, cursor });
		const existing = page.items.filter((session) => session.projectId === projectId && session.kind === "orchestrator" && !session.isTerminated)
			.sort((a, b) => (b.createdAt ?? "").localeCompare(a.createdAt ?? ""))[0];
		if (existing) return existing.id;
		if (!page.page.hasMore) break;
		if (!page.page.nextCursor || seen.has(page.page.nextCursor)) throw new Error("Cloud session list did not advance. Retry before starting an orchestrator.");
		cursor = page.page.nextCursor; seen.add(cursor);
	}
	const [{ project }, userConnections] = await Promise.all([
		client.getProject(orgId, projectId),
		client.listUserProviderConnections(),
	]);
	const ready = new Set(readyHarnesses(userConnections));
	const chosen = projectHarness(project);
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
		...cloudRoleDefaults(project.config, "orchestrator", harness),
	}, { idempotencyKey });
	return session.id;
}
