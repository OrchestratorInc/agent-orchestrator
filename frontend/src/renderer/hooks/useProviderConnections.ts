import { useQuery } from "@tanstack/react-query";
import type { CloudCpProviderConnection } from "../lib/cloud-cp";
import { CLOUD_AGENT_PROVIDERS } from "../lib/cloud-agents";
import { useCloudCp } from "./useCloudCp";

export const providerConnectionsQueryKey = ["cloud-provider-connections"] as const;

export function useProviderConnections(enabled = true) {
	const { client, ready } = useCloudCp(enabled);
	return useQuery({
		queryKey: providerConnectionsQueryKey,
		enabled: ready && enabled,
		staleTime: 60_000,
		queryFn: async (): Promise<CloudCpProviderConnection[]> =>
			(await client.listUserProviderConnections()).providerConnections,
	});
}

export function hasValidAgentConnection(connections: CloudCpProviderConnection[] | undefined): boolean {
	return (connections ?? []).some(
		(connection) =>
			connection.validationState === "valid" &&
			(CLOUD_AGENT_PROVIDERS as readonly string[]).includes(connection.provider),
	);
}
