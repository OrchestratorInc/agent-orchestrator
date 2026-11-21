import { useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiErrorException } from "../lib/api-error-exception";
import { clientForSessionHost } from "../lib/host-clients";
import { agentAuthPlansQueryKeyForHost } from "./useAgentAuth";
import { cacheAgentReadiness, ensureAgentReadiness } from "./useAgentReadinessQuery";

export type AgentEndpointStatus = components["schemas"]["AgentEndpointStatus"];
export type SetAgentEndpointRequest = components["schemas"]["SetAgentEndpointRequest"];

// Credentials go straight to the daemon and stay out of React Query's cache.
// The API key is write-only: responses never carry it.

/** Saves agentId's custom API endpoint. An omitted apiKey keeps the saved key. */
export async function setAgentEndpoint(agentId: string, body: SetAgentEndpointRequest, hostId?: string): Promise<AgentEndpointStatus> {
	const { data, error } = await clientForSessionHost(hostId).PUT("/api/v1/agents/{agent}/endpoint", {
		params: { path: { agent: agentId } },
		body,
	});
	if (error || !data) throw apiErrorException(error, "Could not save the custom endpoint.");
	return data.endpoint;
}

/** Removes agentId's custom API endpoint, returning it to its native sign-in. */
export async function clearAgentEndpoint(agentId: string, hostId?: string): Promise<AgentEndpointStatus> {
	const { data, error } = await clientForSessionHost(hostId).DELETE("/api/v1/agents/{agent}/endpoint", {
		params: { path: { agent: agentId } },
	});
	if (error || !data) throw apiErrorException(error, "Could not remove the custom endpoint.");
	return data.endpoint;
}

/** Refreshes the auth plan and a fresh readiness check after an endpoint change. */
export function useRefreshAfterEndpointChange(hostId?: string) {
	const queryClient = useQueryClient();
	return async (agentId: string) => {
		await queryClient.invalidateQueries({ queryKey: agentAuthPlansQueryKeyForHost(hostId) });
		try {
			cacheAgentReadiness(queryClient, await ensureAgentReadiness([agentId], "display", hostId), hostId);
		} catch {
			// The readiness view refreshes on its own schedule; a failed check
			// here must not undo a saved endpoint.
		}
	};
}
