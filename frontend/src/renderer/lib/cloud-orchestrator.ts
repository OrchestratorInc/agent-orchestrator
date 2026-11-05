import type { QueryClient } from "@tanstack/react-query";
import { createRendererCloudCpClient } from "../hooks/useCloudCp";
import type { CloudCpAgentProvider, CloudCpProviderConnection } from "./cloud-cp";
import { defaultCloudAgent } from "./cloud-agents";
import { selectActiveOrganization } from "./cloud-offering";
import { settingsQueryKey, type Settings } from "../hooks/useSettings";
import { seedCreatedFreestyleSession } from "../hooks/useWorkspaceQuery";
import { captureRendererEvent } from "./telemetry";
import { cloudDefaultInterfaceMode, createCloudSession } from "./cloud-session-create";

// A cloud project has no locally-configured orchestrator agent (that config
// lives in the local daemon's project settings), so the launchers must not
// fall through to the project-settings page for it. Instead the orchestrator
// is spawned as a control-plane session in its own sandbox, exactly like a
// cloud worker session; the worker swaps in the orchestrator system prompt
// server-side based on the session kind.
//
// Deliberately hook-free: the launchers (sidebar row, board, topbar, command
// palette) render everywhere, and subscribing them to the cloud session/org
// queries just for this click handler would fire cloud requests on every
// mount. The client is built lazily from the settings query cache instead.
export function selectCloudOrchestratorHarness(
	connections: readonly CloudCpProviderConnection[],
): CloudCpAgentProvider | undefined {
	return (defaultCloudAgent([...connections]) || undefined) as CloudCpAgentProvider | undefined;
}

/** Spawns a cloud orchestrator session for the project and returns its id. */
export async function spawnCloudOrchestrator(queryClient: QueryClient, projectId: string): Promise<string> {
	const settings = queryClient.getQueryData<Settings>(settingsQueryKey);
	const baseUrl = settings?.cloudControlPlaneUrl ?? "";
	if (baseUrl === "") throw new Error("The cloud control plane is not configured.");
	const client = createRendererCloudCpClient(baseUrl);
	// The active org mirrors useCloudOrg (selectActiveOrganization). A cloud
	// project can only exist inside an org, so signed-in users spawning from one
	// always have it.
	const me = await client.me();
	const orgId = selectActiveOrganization(me.organizations, me.activeOrganizationId)?.id;
	if (orgId === undefined) throw new Error("No cloud organization is available.");
	const { providerConnections: connections } = await client.listUserProviderConnections();
	const harness = selectCloudOrchestratorHarness(connections);
	if (!harness) throw new Error("Connect a Cloud coding agent before spawning an orchestrator.");
	try {
		// No provider: the control plane runs the session on the project's stored
		// provider (config.sandboxProvider), else the org's default.
		// The control plane resolves its interface as the local daemon does (an
		// orchestrator starts in Chat where its harness can) and keeps the
		// user's default for the workers it spawns.
		const { session } = await createCloudSession(client, orgId, {
			projectId,
			kind: "orchestrator",
			harness,
			displayName: "Orchestrator",
			// Role instructions are standing system configuration assembled by the
			// worker; do not duplicate them as a visible user message.
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
