import { useQuery } from "@tanstack/react-query";
import type { CloudCpOrgCoderConfig } from "../lib/cloud-cp";
import { useCloudCp } from "./useCloudCp";
import { useCloudOffering } from "./useCloudOffering";
import { useCloudOrg } from "./useCloudOrg";

export const orgCoderConfigQueryKey = ["cloud-org-coder-config"] as const;

export function useOrgCoderConfig(enabled = true, orgId?: string) {
	const { client, ready } = useCloudCp();
	const { org } = useCloudOrg();
	const { customCoder } = useCloudOffering();
	orgId = orgId?.trim() || (org?.id ?? "");
	return useQuery({
		queryKey: [...orgCoderConfigQueryKey, orgId],
		enabled: enabled && customCoder && ready && orgId !== "",
		staleTime: 60_000,
		queryFn: async (): Promise<CloudCpOrgCoderConfig | null> => {
			const response = await client.getOrgCoderConfig(orgId);
			return response.coderConfig
				? { ...response.coderConfig, tokenSet: true, editable: response.editable !== false }
				: null;
		},
	});
}
