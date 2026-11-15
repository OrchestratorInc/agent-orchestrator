import { useQuery } from "@tanstack/react-query";
import type { CloudCpBillingSummary, CloudCpMeResponse, CloudCpOrganization } from "../lib/cloud-cp/types";
import {
	CODER_PROVIDER,
	canManageBilling,
	cloudMeQueryKey,
	offersProviderChoice,
	resolveCloudOffering,
	type ResolvedCloudOffering,
} from "../lib/cloud-offering";
import { useCloudCp } from "./useCloudCp";
import { useCloudOrg } from "./useCloudOrg";

export const cloudBillingQueryKey = ["cloud-billing"] as const;

export interface UseCloudOfferingResult extends Omit<ResolvedCloudOffering, "source"> {
	source: ResolvedCloudOffering["source"] | undefined;
	hasCoder: boolean;
	offersProviderChoice: boolean;
	selfServeBilling: boolean;
	canManageBilling: boolean;
	chatHarnesses: string[];
	agentSwitchHarnesses: string[];
	agentAccounts: boolean;
	org: CloudCpOrganization | undefined;
	ready: boolean;
	loaded: boolean;
	isLoading: boolean;
	isError: boolean;
}

const EMPTY: ResolvedCloudOffering = {
	providers: [],
	defaultProvider: "",
	kind: "none",
	billingMode: undefined,
	features: [],
	maxAttachmentBytes: undefined,
	customCoder: false,
	artifacts: false,
	coderWorkerWorkingFolder: false,
	source: "fallback",
};

export function useCloudOffering(enabled = true, forOrgId?: string): UseCloudOfferingResult {
	const { client, ready, baseUrl, userId } = useCloudCp(enabled);
	const { org } = useCloudOrg(enabled);
	const orgId = forOrgId || org?.id || "";
	const me = useQuery<CloudCpMeResponse>({
		queryKey: [...cloudMeQueryKey, baseUrl, userId],
		enabled: ready,
		staleTime: 30_000,
		refetchOnWindowFocus: true,
		retry: 1,
		queryFn: ({ signal }) => client.me({ signal }),
	});
	const needsBillingFallback = resolveCloudOffering(me.data, undefined, false, orgId)?.source === "fallback";
	const billing = useQuery<CloudCpBillingSummary>({
		queryKey: [...cloudBillingQueryKey, orgId],
		enabled: ready && needsBillingFallback && orgId !== "",
		queryFn: ({ signal }) => client.getBilling(orgId, { signal }),
		staleTime: 30_000,
		retry: 1,
	});
	const resolved = resolveCloudOffering(me.data, billing.data, billing.isError, orgId || undefined) ?? EMPTY;
	const loaded = me.data !== undefined && resolved.billingMode !== undefined;
	return {
		...resolved,
		source: me.data ? resolved.source : undefined,
		hasCoder: resolved.providers.includes(CODER_PROVIDER),
		offersProviderChoice: me.data !== undefined && offersProviderChoice(resolved),
		selfServeBilling: resolved.billingMode === "self_serve",
		canManageBilling: canManageBilling(org),
		chatHarnesses: me.data?.chatHarnesses ?? [],
		agentSwitchHarnesses: me.data?.agentSwitchHarnesses ?? [],
		agentAccounts: me.data?.agentAccounts === true,
		org,
		ready,
		loaded,
		isLoading: me.isLoading || (needsBillingFallback && billing.isLoading),
		isError: me.isError,
	};
}
