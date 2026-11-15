import type { QueryClient } from "@tanstack/react-query";
import { createRendererCloudCpClient } from "../hooks/useCloudCp";
import type { CloudCpAgentProvider, CloudCpProviderConnection } from "./cloud-cp";
import { CLOUD_AGENT_PROVIDERS } from "./cloud-agents";
import { selectActiveOrganization } from "./cloud-offering";
import { settingsQueryKey, type Settings } from "../hooks/useSettings";
import { seedCreatedFreestyleSession } from "../hooks/useWorkspaceQuery";
import { captureRendererEvent } from "./telemetry";
import { cloudDefaultInterfaceMode, createCloudSession } from "./cloud-session-create";

const ORCHESTRATOR_PREFERRED_HARNESSES: readonly string[] = ["codex"];
const CLOUD_ORCHESTRATOR_HARNESS_PRIORITY: readonly CloudCpAgentProvider[] = [
	...ORCHESTRATOR_PREFERRED_HARNESSES,
	...CLOUD_AGENT_PROVIDERS.filter((provider) => !ORCHESTRATOR_PREFERRED_HARNESSES.includes(provider)),
] as readonly CloudCpAgentProvider[];

function connectedProviders(
	connections: readonly CloudCpProviderConnection[],
): Set<string> {
	return new Set(
		connections
			.filter((connection) => connection.label === "default" && connection.validationState === "valid")
			.map((connection) => connection.provider),
	);
}

export function selectCloudOrchestratorHarness(
	connections: readonly CloudCpProviderConnection[],
): CloudCpAgentProvider | undefined {
	const connected = connectedProviders(connections);
	return CLOUD_ORCHESTRATOR_HARNESS_PRIORITY.find((harness) => connected.has(harness));
}

export function resolveConfiguredOrchestratorHarness(
	project: { config?: Record<string, unknown> } | undefined,
	connections: readonly CloudCpProviderConnection[],
): CloudCpAgentProvider | undefined {
	const orchestrator = (project?.config as { orchestrator?: { agent?: unknown } } | undefined)?.orchestrator;
	const agent = typeof orchestrator?.agent === "string" ? orchestrator.agent.trim() : "";
	if (agent === "") return undefined;
	return connectedProviders(connections).has(agent as CloudCpAgentProvider)
		? (agent as CloudCpAgentProvider)
		: undefined;
}

export async function spawnCloudOrchestrator(queryClient: QueryClient, projectId: string): Promise<string> {
	const settings = queryClient.getQueryData<Settings>(settingsQueryKey);
	const baseUrl = settings?.cloudControlPlaneUrl ?? "";
	if (baseUrl === "") throw new Error("The cloud control plane is not configured.");
	const client = createRendererCloudCpClient(baseUrl);
	const me = await client.me();
	const orgId = selectActiveOrganization(me.organizations, me.activeOrganizationId)?.id;
	if (orgId === undefined) throw new Error("No cloud organization is available.");
	const { providerConnections: connections } = await client.listUserProviderConnections();
	const projects = await client.listProjects(orgId, { limit: 100 }).catch(() => null);
	const project = projects?.items.find((candidate) => candidate.id === projectId);
	const harness = resolveConfiguredOrchestratorHarness(project, connections) ?? selectCloudOrchestratorHarness(connections);
	if (!harness) throw new Error("Connect a Cloud coding agent before spawning an orchestrator.");
	try {
		const { session } = await createCloudSession(client, orgId, {
			projectId,
			kind: "orchestrator",
			harness,
			displayName: "Orchestrator",
			prompt: "",
			defaultInterfaceMode: cloudDefaultInterfaceMode(settings),
		});
		seedCreatedFreestyleSession(queryClient, baseUrl, orgId, session);
		void captureRendererEvent("ao.renderer.cloud_orchestrator_spawn_succeeded", { project_id: projectId });
		return session.id;
	} catch (error) {
		void captureRendererEvent("ao.renderer.cloud_orchestrator_spawn_failed", { project_id: projectId });
		throw error;
	}
}
