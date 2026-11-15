import { focusManager, queryOptions, useQuery, type Query, type QueryClient, type UseQueryResult } from "@tanstack/react-query";
import { useCallback } from "react";
import { cloudModelLabel } from "../lib/agent-model-choices";
import { CLOUD_AGENT_PROVIDERS } from "../lib/cloud-agents";
import type { CloudCpAgentProvider, CloudCpClient, CloudCpProviderConnection } from "../lib/cloud-cp";
import type { AgentModelCatalog } from "./useAgentModelsQuery";
import { cloudModelCatalogGap, type CloudModelCatalog, type CloudModelCatalogGap } from "../lib/cloud-model-catalog";
import { useCloudCp } from "./useCloudCp";
import { useProviderConnections } from "./useProviderConnections";

export const CLOUD_MODEL_CATALOG_QUERY = "cloud-agent-models";

export function cloudModelCatalogQueryPrefix(baseUrl: string, userId: string, orgId: string, agent: string) {
	return [CLOUD_MODEL_CATALOG_QUERY, baseUrl, userId, orgId, agent] as const;
}

export const UNVERIFIED_CATALOG_REFRESH_MS = 15_000;
export const MAX_UNVERIFIED_CATALOG_REFRESH_MS = 5 * 60_000;

let focusEpoch = 0;
focusManager.subscribe((focused) => {
	if (focused) focusEpoch += 1;
});
const releaseStreaks = new WeakMap<object, { epoch: number; base: number }>();

export function unverifiedCatalogRefreshMs(query: Query<CloudModelCatalog, Error, CloudModelCatalog, readonly unknown[]>): number | false {
	if (query.state.data?.source !== "release") return false;
	let streak = releaseStreaks.get(query);
	if (!streak || streak.epoch !== focusEpoch) {
		const count = query.state.dataUpdateCount;
		streak = { epoch: focusEpoch, base: query.state.fetchStatus === "fetching" ? count : count - 1 };
		releaseStreaks.set(query, streak);
	}
	const reads = Math.max(1, query.state.dataUpdateCount - streak.base);
	return Math.min(UNVERIFIED_CATALOG_REFRESH_MS * 2 ** (reads - 1), MAX_UNVERIFIED_CATALOG_REFRESH_MS);
}

function agentConnection(connections: CloudCpProviderConnection[] | undefined, agent: string) {
	return connections?.find(
		(candidate) => candidate.provider === agent && candidate.label === "default" && candidate.validationState === "valid",
	);
}

export function cloudModelCatalogQueryOptions(scope: {
	client: CloudCpClient;
	baseUrl: string;
	userId: string;
	orgId: string;
	agent: string;
	connection: CloudCpProviderConnection | undefined;
}) {
	return queryOptions({
		queryKey: [
			...cloudModelCatalogQueryPrefix(scope.baseUrl, scope.userId, scope.orgId, scope.agent),
			scope.connection?.id ?? "",
			scope.connection?.updatedAt ?? "",
		],
		queryFn: ({ signal }): Promise<CloudModelCatalog> =>
			scope.client.listUserAgentModels(scope.orgId, scope.agent as CloudCpAgentProvider, { signal }),
		staleTime: 30_000,
		refetchOnWindowFocus: (query) => query.state.data?.source === "release" ? "always" : true,
		refetchOnReconnect: false,
		refetchInterval: unverifiedCatalogRefreshMs,
		retry: false,
	});
}

export function cloudCatalogAsAgentCatalog(agent: string, catalog: CloudModelCatalog): AgentModelCatalog {
	return {
		agentId: agent,
		allowCustom: true,
		customModelEntry: "direct",
		fetchedAt: "",
		models: catalog.models.map((item) => ({
			id: item.id,
			label: cloudModelLabel(agent, item),
			isDefault: item.default,
			efforts: item.efforts,
			defaultEffort: item.defaultEffort,
		})),
		selectionMode: "catalog",
		source: "cloud",
		stale: false,
	};
}

export function useCloudAgentCatalogOptions(orgId: string | undefined) {
	const { client, baseUrl, userId } = useCloudCp();
	const connections = useProviderConnections();
	return useCallback((agent: string) => {
		if (!orgId || !(CLOUD_AGENT_PROVIDERS as readonly string[]).includes(agent)) return undefined;
		return {
			...cloudModelCatalogQueryOptions({ client, baseUrl, userId, orgId, agent, connection: agentConnection(connections.data, agent) }),
			select: (catalog: CloudModelCatalog) => cloudCatalogAsAgentCatalog(agent, catalog),
		};
	}, [baseUrl, client, connections.data, orgId, userId]);
}

export interface CloudModelCatalogState {
	catalog: CloudModelCatalog | undefined;
	gap: CloudModelCatalogGap | undefined;
	loading: boolean;
	isFetching: boolean;
	query: UseQueryResult<CloudModelCatalog>;
}

export function useCloudModelCatalog({ orgId, agent, enabled }: {
	orgId: string | undefined;
	agent: string;
	enabled: boolean;
}): CloudModelCatalogState {
	const { client, baseUrl, userId } = useCloudCp();
	const connections = useProviderConnections();
	const supported = (CLOUD_AGENT_PROVIDERS as readonly string[]).includes(agent);
	const active = enabled && supported && Boolean(orgId);
	const query = useQuery({
		...cloudModelCatalogQueryOptions({ client, baseUrl, userId, orgId: orgId ?? "", agent, connection: agentConnection(connections.data, agent) }),
		enabled: active && (connections.data !== undefined || connections.isError),
	});
	const catalog = active ? query.data : undefined;
	return {
		catalog,
		gap: active && !catalog && query.isError ? cloudModelCatalogGap(query.error) : undefined,
		loading: active && catalog === undefined && !query.isError,
		isFetching: query.isFetching,
		query,
	};
}

export function invalidateCloudModelCatalogs(
	queryClient: QueryClient,
	scope: { baseUrl: string; userId: string; orgId: string; agent: string },
): Promise<void> {
	return queryClient.invalidateQueries({
		queryKey: cloudModelCatalogQueryPrefix(scope.baseUrl, scope.userId, scope.orgId, scope.agent),
	});
}
