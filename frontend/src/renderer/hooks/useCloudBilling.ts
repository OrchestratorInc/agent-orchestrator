import { useQuery } from "@tanstack/react-query";
import type { CloudCpBillingSummary } from "../lib/cloud-cp/types";
import { useCloudSession } from "../lib/cloud-session";
import { useCloudCp } from "./useCloudCp";
import { useCloudOrg } from "./useCloudOrg";

export const cloudBillingQueryKey = ["cloud-billing"] as const;

/** The signed-in org's plan, limits and usage. Undefined while signed out or loading. */
export function useCloudBilling(options: { pollUntil?: number } = {}) {
	const { status } = useCloudSession();
	const { client, ready } = useCloudCp();
	const { org } = useCloudOrg();
	const orgId = org?.id ?? "";
	const query = useQuery<CloudCpBillingSummary>({
		queryKey: [...cloudBillingQueryKey, orgId],
		enabled: ready && status === "authenticated" && orgId !== "",
		queryFn: ({ signal }) => client.getBilling(orgId, { signal }),
		staleTime: 30_000,
		refetchOnWindowFocus: true,
		refetchInterval: () => (options.pollUntil && Date.now() < options.pollUntil ? 5_000 : false),
	});
	return { ...query, orgId };
}

/** The paid plan to show beside the user's name, or null for none. */
export function paidPlanBadge(summary: CloudCpBillingSummary | undefined): string | null {
	if (!summary?.enabled || !summary.entitled || summary.exempt) return null;
	const plan = summary.limits?.plan ?? summary.plan;
	if (!plan || plan === "free") return null;
	return plan.charAt(0).toUpperCase() + plan.slice(1);
}
