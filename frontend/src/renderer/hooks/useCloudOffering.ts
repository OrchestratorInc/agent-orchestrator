/**
 * The active organization's AO Cloud offering (GET /me `offering`): which
 * sandbox providers it may use, its default provider, its product kind and how
 * it pays. Every B2B/B2C gate in the renderer reads this one hook.
 *
 * A control plane that predates `offering` is read through a fallback: the
 * providers come from `/me` sandboxProviders and the billing mode from the
 * `/billing` summary (enabled/exempt), so the app keeps working against it.
 */

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
	/** The org's providers include coder: Coder project options apply. */
	hasCoder: boolean;
	/** More than one server-sent provider: a project picks where its sessions run. */
	offersProviderChoice: boolean;
	/** Billing UI (plans, usage, plan badge, plan prompts) applies. */
	selfServeBilling: boolean;
	/** The signed-in user may buy, change or reset the org's plan. */
	canManageBilling: boolean;
	/** Harnesses a session can start in Chat with (`/me` chatHarnesses). */
	chatHarnesses: string[];
	org: CloudCpOrganization | undefined;
	/** Mirrors useCloudCp().ready so callers can gate on one hook. */
	ready: boolean;
	/** The offering has resolved (including the fallback's billing read). */
	loaded: boolean;
	isLoading: boolean;
	/** `/me` failed; the offering cannot resolve until it is retried. */
	isError: boolean;
}

const EMPTY: ResolvedCloudOffering = {
	providers: [],
	defaultProvider: "",
	kind: "none",
	billingMode: undefined,
	features: [],
	customCoder: false,
	artifacts: false,
	coderWorkerWorkingFolder: false,
	source: "fallback",
};

// enabled=false keeps a caller that may not need the offering (a remote host's
// board) from reading this machine's settings, as useCloudCp(enabled) does.
export function useCloudOffering(enabled = true): UseCloudOfferingResult {
	const { client, ready, baseUrl, userId } = useCloudCp(enabled);
	const { org } = useCloudOrg(enabled);
	const orgId = org?.id ?? "";
	const me = useQuery<CloudCpMeResponse>({
		queryKey: [...cloudMeQueryKey, baseUrl, userId],
		enabled: ready,
		// Matches useCloudOrg: entitlements can change while the app is open
		// (an org gains coder, moves to a contract), so refresh on focus.
		staleTime: 30_000,
		refetchOnWindowFocus: true,
		retry: 1,
		queryFn: ({ signal }) => client.me({ signal }),
	});
	// Only an older control plane without `offering` needs /billing to learn the
	// billing mode. It shares useCloudBilling's cache entry.
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
		org,
		ready,
		loaded,
		isLoading: me.isLoading || (needsBillingFallback && billing.isLoading),
		isError: me.isError,
	};
}
