import { useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiErrorException } from "../lib/api-error-exception";
import { clientForSessionHost } from "../lib/host-clients";

export type ProviderKey = components["schemas"]["ProviderKey"];
export type ProviderKeyCheck = components["schemas"]["ProviderKeyCheck"];

export const providerKeysQueryKey = (hostId?: string) => hostId ? ["provider-keys", hostId] as const : ["provider-keys"] as const;

async function fetchProviderKeys(hostId?: string): Promise<ProviderKey[]> {
	const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/agents/provider-keys");
	if (error || !data) throw apiErrorException(error, "Could not load API keys.");
	return data.keys;
}

export function useProviderKeys(hostId?: string, enabled = true) {
	return useQuery({ queryKey: providerKeysQueryKey(hostId), queryFn: () => fetchProviderKeys(hostId), staleTime: 30_000, enabled });
}

export function useInvalidateProviderKeys(hostId?: string) {
	const queryClient = useQueryClient();
	return () => queryClient.invalidateQueries({ queryKey: providerKeysQueryKey(hostId) });
}

export async function removeProviderKey(name: string, hostId?: string): Promise<ProviderKey> {
	const { data, error } = await clientForSessionHost(hostId).DELETE("/api/v1/agents/provider-keys/{name}", { params: { path: { name } } });
	if (error || !data) throw apiErrorException(error, "Could not remove the key.");
	return data;
}

export async function checkProviderKey(name: string, secret: string, hostId?: string): Promise<ProviderKeyCheck> {
	const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/agents/provider-keys/{name}/check", {
		params: { path: { name } }, body: secret ? { secret } : {},
	});
	if (error || !data) throw apiErrorException(error, "Could not check the key.");
	return data;
}

export function providerKeyName(methodId: string): string | undefined {
	return methodId.startsWith("env_") ? methodId.slice(4) : undefined;
}
