import { CloudCpError } from "./cloud-cp";
import type { CloudCpClient, CloudCpCreateSessionRequest, CloudCpInterfaceMode, CloudCpSessionResponse } from "./cloud-cp";
import type { Settings } from "../hooks/useSettings";
import { reportCloudSessionCreateRequested } from "./cloud-telemetry";

/**
 * The default session interface a new cloud orchestrator is created with: the
 * daemon-owned "Default session interface" (Settings > General), which local
 * spawns resolve from too. The control plane resolves the orchestrator's
 * interface from it as the local daemon does (Chat where its harness can) and
 * keeps it for the workers the orchestrator spawns. A task sends the
 * interface it starts in instead (interfaceMode, from the composer). It is
 * always sent, "tui" included: the control plane starts a create without
 * either field (an older desktop's) in the terminal, an orchestrator too.
 */
export function cloudDefaultInterfaceMode(
	settings: Pick<Settings, "defaultSessionMode"> | undefined,
): CloudCpInterfaceMode {
	return settings?.defaultSessionMode === "chat" ? "chat" : "tui";
}

/**
 * Creates a cloud session, with the interface it starts in (interfaceMode)
 * or the user's default session interface (defaultInterfaceMode) when the body
 * carries one. A control plane that predates those fields rejects the whole
 * body with 400 invalid_request before creating anything, so the create is
 * sent again without them and the session starts in the terminal with its
 * prompt on the agent's command line, as every session did there. The user's
 * request is reported once, not once per call.
 */
export async function createCloudSession(
	client: Pick<CloudCpClient, "createSession">,
	orgId: string,
	body: CloudCpCreateSessionRequest,
): Promise<CloudCpSessionResponse> {
	try {
		reportCloudSessionCreateRequested(body);
	} catch {
		// Telemetry must never break the create.
	}
	const { interfaceMode, defaultInterfaceMode, ...withoutInterface } = body;
	if (interfaceMode === undefined && defaultInterfaceMode === undefined) return client.createSession(orgId, body);
	try {
		return await client.createSession(orgId, body);
	} catch (error) {
		if (!isUnknownInterfaceFieldRefusal(error)) throw error;
		return client.createSession(orgId, withoutInterface);
	}
}

/** A control plane's refusal of a create body carrying a field it predates. */
export function isUnknownInterfaceFieldRefusal(error: unknown): boolean {
	return error instanceof CloudCpError && error.status === 400 && error.code === "invalid_request";
}
