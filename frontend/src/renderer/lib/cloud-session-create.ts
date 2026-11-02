import { CloudCpError } from "./cloud-cp";
import type { CloudCpClient, CloudCpCreateSessionRequest, CloudCpInterfaceMode, CloudCpMutationOptions, CloudCpSessionResponse } from "./cloud-cp";
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
	options?: CloudCpMutationOptions,
): Promise<CloudCpSessionResponse> {
	try {
		reportCloudSessionCreateRequested(body);
	} catch {
		// Telemetry must never break the create.
	}
	const send = (request: CloudCpCreateSessionRequest, sendOptions = options) =>
		sendOptions ? client.createSession(orgId, request, sendOptions) : client.createSession(orgId, request);
	const { interfaceMode, defaultInterfaceMode, ...withoutInterface } = body;
	if (interfaceMode === undefined && defaultInterfaceMode === undefined) return send(body);
	try {
		return await send(body);
	} catch (error) {
		if (!isUnknownInterfaceFieldRefusal(error)) throw error;
		// A different body, so its own key: a retry still replays it, but never collides with the refused one.
		return send(withoutInterface, options?.idempotencyKey ? { ...options, idempotencyKey: `${options.idempotencyKey}:tui` } : options);
	}
}

/** A control plane's refusal of a create body carrying a field it predates. */
export function isUnknownInterfaceFieldRefusal(error: unknown): boolean {
	return error instanceof CloudCpError && error.status === 400 && error.code === "invalid_request";
}
