import { queryOptions, useQuery, type QueryClient, type UseQueryResult } from "@tanstack/react-query";
import { useCallback } from "react";
import { cloudModelLabel } from "../lib/agent-model-choices";
import { CLOUD_AGENT_PROVIDERS } from "../lib/cloud-agents";
import type { CloudCpAgentProvider, CloudCpClient, CloudCpProviderConnection } from "../lib/cloud-cp";
import type { AgentModelCatalog } from "./useAgentModelsQuery";
import { cloudModelCatalogGap, type CloudModelCatalog, type CloudModelCatalogGap } from "../lib/cloud-model-catalog";
import { useCloudCp } from "./useCloudCp";
import { useProviderConnections } from "./useProviderConnections";

/** Every Cloud model list query starts with this. */
export const CLOUD_MODEL_CATALOG_QUERY = "cloud-agent-models";

/** The Cloud model lists of one account and agent, any credential. */
export function cloudModelCatalogQueryPrefix(baseUrl: string, userId: string, orgId: string, agent: string) {
	return [CLOUD_MODEL_CATALOG_QUERY, baseUrl, userId, orgId, agent] as const;
}

/**
 * While Cloud only knows the release list it is learning the account's own
 * from a running session, so the list is read again this often until it
 * arrives.
 */
export const UNVERIFIED_CATALOG_REFRESH_MS = 15_000;

/** The default valid credential an account has connected for an agent. */
function agentConnection(connections: CloudCpProviderConnection[] | undefined, agent: string) {
	return connections?.find(
		(candidate) => candidate.provider === agent && candidate.label === "default" && candidate.validationState === "valid",
	);
}

/**
 * The one query for Cloud's model list of an account and agent: keyed by the
 * credential (a new sign-in asks again), read again on focus, and every
 * UNVERIFIED_CATALOG_REFRESH_MS while it is still the release list.
 */
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
		refetchOnWindowFocus: true,
		refetchOnReconnect: false,
		refetchInterval: (query) => query.state.data?.source === "release" ? UNVERIFIED_CATALOG_REFRESH_MS : false,
		retry: false,
	});
}

/** Cloud's list for an agent in the shape model pickers take (as this computer's). */
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

/**
 * Query options for any agent's Cloud model list in one organization, in the
 * pickers' catalog shape, for a picker that lists several agents at once.
 * Undefined without an organization (not a Cloud task) or for an agent Cloud
 * lists no models for.
 */
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
	/** The list, once read. */
	catalog: CloudModelCatalog | undefined;
	/** Why there is none, after the control plane answered without one. */
	gap: CloudModelCatalogGap | undefined;
	/** Asked and not answered yet. */
	loading: boolean;
	isFetching: boolean;
	/** The query itself, for a caller that refreshes and reads the answer. */
	query: UseQueryResult<CloudModelCatalog>;
}

/**
 * Cloud's model list for an agent, for every place that picks a model for a
 * Cloud task or project: the New Task composer, its agent switch and the cloud
 * project's settings. One query and one freshness rule for all of them: keyed
 * by the account's credential for the agent (a new sign-in asks again), read
 * again on focus, and every UNVERIFIED_CATALOG_REFRESH_MS while it is still
 * the release list, so the account's own list shows up as soon as Cloud has it.
 */
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
		// Asked once the connections are known, so their arrival does not ask twice.
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

/**
 * Has every open Cloud model list of the account and agent read again, after
 * something showed what the account runs (a session's own list).
 */
export function invalidateCloudModelCatalogs(
	queryClient: QueryClient,
	scope: { baseUrl: string; userId: string; orgId: string; agent: string },
): Promise<void> {
	return queryClient.invalidateQueries({
		queryKey: cloudModelCatalogQueryPrefix(scope.baseUrl, scope.userId, scope.orgId, scope.agent),
	});
}
