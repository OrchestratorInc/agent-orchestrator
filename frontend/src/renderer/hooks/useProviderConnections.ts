/**
 * Lists the org's coding-agent provider connections (GET
 * /orgs/{orgId}/provider-connections). Used by the onboarding gate to decide
 * whether to prompt for a credential, and invalidated by the credential dialog
 * after a successful connect.
 */

import { useQuery } from "@tanstack/react-query";
import type { CloudCpProviderConnection } from "../lib/cloud-cp";
import { CLOUD_AGENT_PROVIDERS } from "../lib/cloud-agents";
import { useCloudCp } from "./useCloudCp";

export function providerConnectionsQueryKey(orgId: string) {
	return ["cloud-provider-connections", orgId] as const;
}

export function useProviderConnections(orgId: string | undefined) {
	const { client, ready } = useCloudCp();
	return useQuery({
		queryKey: providerConnectionsQueryKey(orgId ?? ""),
		enabled: ready && orgId !== undefined,
		staleTime: 60_000,
		queryFn: async (): Promise<CloudCpProviderConnection[]> => {
			const { providerConnections } = await client.listProviderConnections(orgId as string);
			return providerConnections;
		},
	});
}

/** The caller's personal connections (GET /me/providers), usable in every org. */
export const userProviderConnectionsQueryKey = ["cloud-user-provider-connections"] as const;

export function useUserProviderConnections() {
	const { client, ready } = useCloudCp();
	return useQuery({
		queryKey: userProviderConnectionsQueryKey,
		enabled: ready,
		staleTime: 60_000,
		queryFn: async (): Promise<CloudCpProviderConnection[]> =>
			(await client.listUserProviderConnections()).providerConnections,
	});
}

/**
 * True when at least one coding-agent connection the control plane validated
 * exists. Personal lists also hold non-agent credentials (a GitHub token), which
 * do not count.
 */
export function hasValidAgentConnection(connections: CloudCpProviderConnection[] | undefined): boolean {
	return (connections ?? []).some(
		(connection) =>
			connection.validationState === "valid" &&
			(CLOUD_AGENT_PROVIDERS as readonly string[]).includes(connection.provider),
	);
}
