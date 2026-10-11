import { useQuery } from "@tanstack/react-query";
import type { CloudCpBillingSummary } from "../lib/cloud-cp/types";
import { useCloudSession } from "../lib/cloud-session";
import { useCloudCp } from "./useCloudCp";
import { cloudBillingQueryKey, useCloudOffering } from "./useCloudOffering";

export { cloudBillingQueryKey };

/**
 * The signed-in org's plan, limits and usage. Undefined while signed out or
 * loading, and never fetched for an org that is not on self-serve billing
 * (contract, billing off): those orgs have no plans to show. The fallback for
 * an older control plane reads the same cache entry to learn the billing mode,
 * but only a self-serve org polls it.
 */
export function useCloudBilling(options: { pollUntil?: number } = {}) {
	const { status } = useCloudSession();
	const { client, ready } = useCloudCp();
	const offering = useCloudOffering();
	const orgId = offering.org?.id ?? "";
	const query = useQuery<CloudCpBillingSummary>({
		queryKey: [...cloudBillingQueryKey, orgId],
		enabled: ready && status === "authenticated" && orgId !== "" && offering.selfServeBilling,
		queryFn: ({ signal }) => client.getBilling(orgId, { signal }),
		staleTime: 30_000,
		refetchOnWindowFocus: offering.selfServeBilling,
		refetchInterval: () =>
			offering.selfServeBilling && options.pollUntil && Date.now() < options.pollUntil ? 5_000 : false,
	});
	return { ...query, orgId, offering };
}

/** The paid plan to show beside the user's name, or null for none. */
export function paidPlanBadge(summary: CloudCpBillingSummary | undefined): string | null {
	if (!summary?.enabled || !summary.entitled || summary.exempt) return null;
	if (summary.billingMode !== undefined && summary.billingMode !== "self_serve") return null;
	const plan = summary.limits?.plan ?? summary.plan;
	if (!plan || plan === "free") return null;
	return plan.charAt(0).toUpperCase() + plan.slice(1);
}
