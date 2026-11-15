import { useQuery } from "@tanstack/react-query";
import type { CloudCpOrganization, CloudCpUser } from "../lib/cloud-cp";
import { selectActiveOrganization } from "../lib/cloud-offering";
import { useCloudCp } from "./useCloudCp";

export const cloudOrgQueryKey = ["cloud-org"] as const;

const WORKSPACE_SUFFIX = "'s workspace";
const MAX_WORKSPACE_NAME = 80;

export function orgDisplayNameForAccount(user: Pick<CloudCpUser, "displayName" | "email">): string {
	const displayName = user.displayName.trim();
	const name = (displayName.includes("@") ? "" : displayName.split(/\s+/)[0]) || user.email.split("@")[0]?.trim() || "";
	if (name === "") return "Workspace";
	return name.slice(0, MAX_WORKSPACE_NAME - WORKSPACE_SUFFIX.length) + WORKSPACE_SUFFIX;
}

export interface UseCloudOrgResult {
	org: CloudCpOrganization | undefined;
	isLoading: boolean;
	error: unknown;
	refetch: () => Promise<{ data?: CloudCpOrganization; error?: unknown }>;
	ready: boolean;
}

export function useCloudOrg(enabled = true): UseCloudOrgResult {
	const { client, ready, baseUrl, userId } = useCloudCp(enabled);
	const query = useQuery({
		queryKey: [...cloudOrgQueryKey, baseUrl, userId],
		enabled: ready,
		staleTime: 30_000,
		refetchOnWindowFocus: true,
		refetchOnReconnect: true,
		retry: 1,
		queryFn: async (): Promise<CloudCpOrganization> => {
			const me = await client.me();
			const active = selectActiveOrganization(me.organizations, me.activeOrganizationId);
			if (active !== undefined) return active;
			const created = await client.createOrganization({
				displayName: orgDisplayNameForAccount(me.user),
			});
			return created.organization;
		},
	});
	return {
		org: query.data,
		isLoading: query.isLoading,
		error: query.error ?? undefined,
		refetch: query.refetch,
		ready,
	};
}
