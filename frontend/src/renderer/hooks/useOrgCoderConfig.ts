/**
 * Reads the signed-in org's bring-your-own-Coder connection
 * (GET /orgs/{orgId}/coder-config). The config is org-scoped (it drives every
 * cloud session the org runs on its own Coder), so the query is keyed on the
 * resolved org and reads only the non-secret fields — the API token is never
 * returned by the control plane.
 */

import { useQuery } from "@tanstack/react-query";
import type { CloudCpOrgCoderConfig } from "../lib/cloud-cp";
import { useCloudCp } from "./useCloudCp";
import { useCloudOffering } from "./useCloudOffering";
import { useCloudOrg } from "./useCloudOrg";

export const orgCoderConfigQueryKey = ["cloud-org-coder-config"] as const;

/**
 * Read only for an org whose offering includes coder: the control plane
 * answers 403 provider_forbidden for any other org. `enabled` lets a caller
 * narrow it further.
 */
export function useOrgCoderConfig(enabled = true) {
	const { client, ready } = useCloudCp();
	const { org } = useCloudOrg();
	// The org's own Coder connection exists only for an org with the coder
	// capability; the control plane refuses the read for everyone else.
	const { customCoder } = useCloudOffering();
	const orgId = org?.id ?? "";
	return useQuery({
		queryKey: [...orgCoderConfigQueryKey, orgId],
		enabled: enabled && customCoder && ready && orgId !== "",
		staleTime: 60_000,
		queryFn: async (): Promise<CloudCpOrgCoderConfig | null> => {
			const response = await client.getOrgCoderConfig(orgId);
			// Default to null (never undefined) so React Query accepts the "no
			// config yet" case — the common state for a newly-onboarded org. A stored
			// connection always carries a token (the PUT requires one) and the token
			// itself is never returned, so surface its presence as tokenSet — the flag
			// the templates catalog and the live template fetch unlock on. An older
			// control plane answers only orgs that may save, so absent means editable.
			return response.coderConfig
				? { ...response.coderConfig, tokenSet: true, editable: response.editable !== false }
				: null;
		},
	});
}
